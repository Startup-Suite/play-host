extends Node
## Suite play addon (autoload `SuitePlay`, task 01a0db5f). Inert unless Godot
## was started with the user arg --suite-play-session=<id> after "--".
##
## Listens on 127.0.0.1:<--suite-play-port> for exactly one connection from
## the play host, which carries:
##   host -> game: newline-delimited JSON (see internal/input in play-host):
##     {"t":"key","k":"W","loc":0,"p":true,"e":false}  k = Godot key name
##     {"t":"jb","d":0,"b":0,"p":true,"v":1}          joypad button, device d
##     {"t":"ja","d":0,"a":0,"v":-0.5}               joypad axis, device d
##     {"t":"release_all"}  {"t":"probe","seq":N}  {"t":"export","on":true}
##   game -> host: frames while export is on (frame path C, stage 1).
## Frame record (little-endian): "SPF1", u32 width, u32 height, u32 kind
## (1 = Image.FORMAT_RGBA8 from get_image, 2 = RD texture bytes), u32 length, bytes.
##
## All input goes through Input.parse_input_event: no ViGEm, no virtual
## device driver. Mapping and edge-diffing are done by the host (Go, unit
## tested); this file only resolves key names and builds the InputEvents.
##
## Probe marker: a 4x4 grid of 16x16 px cells in the top-left corner, row-major,
## MSB first, value = seq << 4 | check(seq), check = (s ^ s>>4 ^ s>>8 ^ 0xA) & 0xF.
## Drawn for 2 frames per probe, black otherwise. Pinned by internal/probe in Go.

const CELLS := 4
const CELL_PX := 16
const PROBE_FRAMES := 2
const MAX_SEQ := 4095

var session_id := ""
var port := 0
## "image" | "async" | "none". The spike passed --suite-play-export and
## exported always; the play host leaves it unset and switches export on and
## off with {"t":"export"} so an unwatched game spends no GPU on readback.
var export_mode := "image"
var export_forced := false
var exporting := false
var _server := TCPServer.new()
var _peer: StreamPeerTCP
var _rx := PackedByteArray()
var _layer: CanvasLayer
var _cells: Array[ColorRect] = []
var _probe_left := 0
var _rd_tex := RID()
var _async_inflight := 0
var frames_sent := 0
var probes_seen := 0
var _keycodes := {}  # name -> Key
var _held_keys := {}  # "keycode:loc" -> [keycode, loc]
var _held_buttons := {}  # "d:b" -> [d, b]
var _held_axes := {}  # "d:a" -> [d, a]
var _checked_key := false
var _checked_axis := false


func _ready() -> void:
	for a in OS.get_cmdline_user_args():
		if a.begins_with("--suite-play-session="):
			session_id = a.get_slice("=", 1)
		elif a.begins_with("--suite-play-port="):
			port = int(a.get_slice("=", 1))
		elif a.begins_with("--suite-play-export="):
			export_mode = a.get_slice("=", 1)
			export_forced = export_mode != "none"
		elif a.begins_with("--suite-play-fps="):
			Engine.max_fps = int(a.get_slice("=", 1))
	exporting = export_forced
	if session_id == "" or port <= 0:
		set_process(false)
		return
	if Engine.max_fps == 0:
		Engine.max_fps = 60
	process_mode = Node.PROCESS_MODE_ALWAYS
	var err := _server.listen(port, "127.0.0.1")
	print("suite_play: session=%s port=%d export=%s listen=%s" % [session_id, port, export_mode, error_string(err)])
	_build_marker()
	RenderingServer.frame_post_draw.connect(_on_frame_post_draw)


func _build_marker() -> void:
	_layer = CanvasLayer.new()
	_layer.layer = 128
	add_child(_layer)
	for i in CELLS * CELLS:
		var r := ColorRect.new()
		r.color = Color.BLACK
		r.position = Vector2((i % CELLS) * CELL_PX, (i / CELLS) * CELL_PX)
		r.size = Vector2(CELL_PX, CELL_PX)
		r.mouse_filter = Control.MOUSE_FILTER_IGNORE
		_layer.add_child(r)
		_cells.append(r)


static func check(seq: int) -> int:
	var s := seq & MAX_SEQ
	return (s ^ (s >> 4) ^ (s >> 8) ^ 0xA) & 0xF


func show_probe(seq: int) -> void:
	var v := ((seq & MAX_SEQ) << 4) | check(seq)
	for i in CELLS * CELLS:
		_cells[i].color = Color.WHITE if (v >> (15 - i)) & 1 == 1 else Color.BLACK
	_probe_left = PROBE_FRAMES
	probes_seen += 1


func _clear_probe() -> void:
	for r in _cells:
		r.color = Color.BLACK


func _process(_delta: float) -> void:
	if _server.is_connection_available():
		if _peer != null:
			_peer.disconnect_from_host()
		_peer = _server.take_connection()
		_peer.set_no_delay(true)
		print("suite_play: host connected")
	if _peer == null:
		return
	_peer.poll()
	if _peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
		print("suite_play: host link closed; releasing input")
		release_all()
		_peer = null
		exporting = export_forced
		return
	var n := _peer.get_available_bytes()
	if n > 0:
		var got := _peer.get_partial_data(n)
		if got[0] == OK:
			_rx.append_array(got[1])
	while true:
		var nl := _rx.find(10)
		if nl < 0:
			break
		var line := _rx.slice(0, nl).get_string_from_utf8()
		_rx = _rx.slice(nl + 1)
		handle_line(line)


## One host line. Public so the headless self-test can drive it.
func handle_line(line: String) -> void:
	var msg = JSON.parse_string(line)
	if typeof(msg) != TYPE_DICTIONARY:
		return
	match str(msg.get("t", "")):
		"probe":
			show_probe(int(msg.get("seq", 0)))
		"key":
			_key(str(msg.get("k", "")), int(msg.get("loc", 0)), bool(msg.get("p", false)), bool(msg.get("e", false)))
		"jb":
			_joy_button(int(msg.get("d", 0)), int(msg.get("b", 0)), bool(msg.get("p", false)), float(msg.get("v", 0.0)))
		"ja":
			_joy_axis(int(msg.get("d", 0)), int(msg.get("a", 0)), float(msg.get("v", 0.0)))
		"release_all":
			release_all()
		"export":
			exporting = bool(msg.get("on", false)) or export_forced
			print("suite_play: export %s" % exporting)


## Key name (as OS.find_keycode_from_string reads it) -> Key, cached. 0 if unknown.
## The host sends platform-neutral names; Godot spells META per platform
## ("Windows" on Windows, "Command" on macOS), so it is special-cased here.
## Measured on wave: without this, "Meta" resolved to KEY_NONE.
func keycode_for(name: String) -> int:
	if not _keycodes.has(name):
		_keycodes[name] = KEY_META if name == "Meta" else OS.find_keycode_from_string(name)
	return int(_keycodes[name])


func _key(name: String, loc: int, pressed: bool, echo: bool) -> void:
	var code := keycode_for(name)
	if code == KEY_NONE:
		return
	var ev := InputEventKey.new()
	ev.keycode = code
	ev.physical_keycode = code
	ev.location = loc
	ev.pressed = pressed
	ev.echo = echo
	var id := "%d:%d" % [code, loc]
	if pressed:
		_held_keys[id] = [code, loc]
	else:
		_held_keys.erase(id)
	Input.parse_input_event(ev)
	if not _checked_key:
		_checked_key = true
		Input.flush_buffered_events()
		print("suite_play: first key %s pressed=%s -> Input.is_physical_key_pressed=%s" % [name, pressed, Input.is_physical_key_pressed(code)])


func _joy_button(device: int, button: int, pressed: bool, value: float) -> void:
	var ev := InputEventJoypadButton.new()
	ev.device = device
	ev.button_index = button
	ev.pressed = pressed
	ev.pressure = value
	var id := "%d:%d" % [device, button]
	if pressed:
		_held_buttons[id] = [device, button]
	else:
		_held_buttons.erase(id)
	Input.parse_input_event(ev)


func _joy_axis(device: int, axis: int, value: float) -> void:
	var ev := InputEventJoypadMotion.new()
	ev.device = device
	ev.axis = axis
	ev.axis_value = value
	var id := "%d:%d" % [device, axis]
	if absf(value) > 0.0:
		_held_axes[id] = [device, axis]
	else:
		_held_axes.erase(id)
	Input.parse_input_event(ev)
	if not _checked_axis:
		_checked_axis = true
		Input.flush_buffered_events()
		print("suite_play: first axis d%d a%d v=%.3f -> Input.get_joy_axis=%.3f" % [device, axis, value, Input.get_joy_axis(device, axis)])


## Releases every key, button and axis this addon pressed.
func release_all() -> void:
	for k in _held_keys.values():
		var ev := InputEventKey.new()
		ev.keycode = k[0]
		ev.physical_keycode = k[0]
		ev.location = k[1]
		ev.pressed = false
		Input.parse_input_event(ev)
	_held_keys.clear()
	for b in _held_buttons.values():
		var ev := InputEventJoypadButton.new()
		ev.device = b[0]
		ev.button_index = b[1]
		ev.pressed = false
		Input.parse_input_event(ev)
	_held_buttons.clear()
	for a in _held_axes.values():
		var ev := InputEventJoypadMotion.new()
		ev.device = a[0]
		ev.axis = a[1]
		ev.axis_value = 0.0
		Input.parse_input_event(ev)
	_held_axes.clear()


func _on_frame_post_draw() -> void:
	if _probe_left > 0:
		_probe_left -= 1
		if _probe_left == 0:
			_clear_probe()
	if _peer == null or not exporting:
		return
	match export_mode:
		"image":
			var img := get_viewport().get_texture().get_image()
			if img.get_format() != Image.FORMAT_RGBA8:
				img.convert(Image.FORMAT_RGBA8)
			_send_frame(img.get_width(), img.get_height(), 1, img.get_data())
		"async":
			_request_async()


func _request_async() -> void:
	if _async_inflight >= 2:
		return
	var rd := RenderingServer.get_rendering_device()
	if rd == null:
		return
	if not _rd_tex.is_valid():
		_rd_tex = RenderingServer.texture_get_rd_texture(get_viewport().get_texture().get_rid())
		var fmt := rd.texture_get_format(_rd_tex)
		print("suite_play: rd texture format=%d %dx%d" % [fmt.format, fmt.width, fmt.height])
	var size := get_viewport().get_texture().get_size()
	_async_inflight += 1
	rd.texture_get_data_async(_rd_tex, 0, func(data: PackedByteArray) -> void:
		_async_inflight -= 1
		_send_frame(int(size.x), int(size.y), 2, data))


func _send_frame(w: int, h: int, kind: int, data: PackedByteArray) -> void:
	if _peer == null:
		return
	var hdr := StreamPeerBuffer.new()
	hdr.put_data("SPF1".to_ascii_buffer())
	hdr.put_u32(w)
	hdr.put_u32(h)
	hdr.put_u32(kind)
	hdr.put_u32(data.size())
	_peer.put_data(hdr.data_array)
	_peer.put_data(data)
	frames_sent += 1
