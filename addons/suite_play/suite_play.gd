extends Node
## Suite play addon (autoload `SuitePlay`, task 01a0db5f). Inert unless Godot
## was started with the user arg --suite-play-session=<id> after "--".
##
## Listens on 127.0.0.1:<--suite-play-port> for exactly one connection from
## the play host, which carries:
##   host -> game: newline-delimited JSON (see internal/input in play-host):
##     {"t":"key","d":0,"k":"W","loc":0,"p":true,"e":false}  k = Godot key name, device d
##     {"t":"jb","d":0,"b":0,"p":true,"v":1}                joypad button, device d
##     {"t":"ja","d":0,"a":0,"v":-0.5}                     joypad axis, device d
##     {"t":"release","d":0}  releases what device d holds
##     {"t":"release_all"}  {"t":"probe","seq":N}  {"t":"export","on":true}
## d is the player SLOT the host bound the input to (P1 = 0), never a value
## the browser chose. A key line without d is slot 0 (a v1 host).
##
## Device ids (task 01a0dbd6, measured on Godot 4.7.2): a key event's default
## device is 16 (the keyboard id), and the built-in ui_* actions are bound to
## device 16, so a key event with device 0 does NOT match ui_accept. So keys
## from slot d carry device <default> + d (P1 keeps the default, exactly what
## a v1 key had, so every InputMap action still fires for P1), and pads carry
## device d. Every event also carries meta "suite_play_slot" = d.
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
## A multi-player host forwards seq = slot << 10 | (seq & 0x3FF), so the top 2
## bits name the slot; this file draws whatever seq it is given.
##
## For games (task 01a0dbd6, recorded for Voltron): the slot of any event is
## event.get_meta("suite_play_slot", 0); for a key it is also
## event.device - 16, for a pad event.device. KEY POLLING has no device:
## Input.is_physical_key_pressed / is_key_pressed / is_action_pressed are
## global, so two keyboard players are distinct ONLY in _input /
## _unhandled_input. An action bound to device 16 (the ui_* defaults) fires
## for P1's keys only; an action set to "All Devices" fires for every slot.
## Pads are distinct by polling too, through Input.get_joy_axis(d, ...) and
## is_joy_button_pressed(d, ...).

const CELLS := 4
const CELL_PX := 16
const PROBE_FRAMES := 2
const MAX_SEQ := 4095
const SLOT_META := &"suite_play_slot"

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
var _held_keys := {}  # "d:keycode:loc" -> [keycode, loc, d]
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
			_key(str(msg.get("k", "")), int(msg.get("loc", 0)), bool(msg.get("p", false)), bool(msg.get("e", false)), int(msg.get("d", 0)))
		"jb":
			_joy_button(int(msg.get("d", 0)), int(msg.get("b", 0)), bool(msg.get("p", false)), float(msg.get("v", 0.0)))
		"ja":
			_joy_axis(int(msg.get("d", 0)), int(msg.get("a", 0)), float(msg.get("v", 0.0)))
		"release":
			release_device(int(msg.get("d", 0)))
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


func _key(name: String, loc: int, pressed: bool, echo: bool, device: int = 0) -> void:
	var code := keycode_for(name)
	if code == KEY_NONE:
		return
	var ev := _key_event(code, loc, device)
	ev.pressed = pressed
	ev.echo = echo
	var id := "%d:%d:%d" % [device, code, loc]
	if pressed:
		_held_keys[id] = [code, loc, device]
	else:
		_held_keys.erase(id)
	Input.parse_input_event(ev)
	if not _checked_key:
		_checked_key = true
		Input.flush_buffered_events()
		print("suite_play: first key %s pressed=%s -> Input.is_physical_key_pressed=%s" % [name, pressed, Input.is_physical_key_pressed(code)])


## A key event for slot `slot`: the default key device + slot, slot in meta.
static func _key_event(code: int, loc: int, slot: int) -> InputEventKey:
	var ev := InputEventKey.new()
	ev.device += slot
	ev.keycode = code
	ev.physical_keycode = code
	ev.location = loc
	ev.set_meta(SLOT_META, slot)
	return ev


func _joy_button(device: int, button: int, pressed: bool, value: float) -> void:
	var ev := InputEventJoypadButton.new()
	ev.device = device
	ev.set_meta(SLOT_META, device)
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
	ev.set_meta(SLOT_META, device)
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


## Releases every key, button and axis this addon pressed, on every device.
func release_all() -> void:
	_release(-1)


## Releases what device d holds, and nothing another device holds (one
## player leaving, or being taken over, must not lift another's keys).
func release_device(d: int) -> void:
	_release(d)


## device -1 = all.
func _release(device: int) -> void:
	for id in _held_keys.keys():
		var k: Array = _held_keys[id]
		if device >= 0 and k[2] != device:
			continue
		var ev := _key_event(k[0], k[1], k[2])
		ev.pressed = false
		Input.parse_input_event(ev)
		_held_keys.erase(id)
	for id in _held_buttons.keys():
		var b: Array = _held_buttons[id]
		if device >= 0 and b[0] != device:
			continue
		var ev := InputEventJoypadButton.new()
		ev.device = b[0]
		ev.set_meta(SLOT_META, b[0])
		ev.button_index = b[1]
		ev.pressed = false
		Input.parse_input_event(ev)
		_held_buttons.erase(id)
	for id in _held_axes.keys():
		var a: Array = _held_axes[id]
		if device >= 0 and a[0] != device:
			continue
		var ev := InputEventJoypadMotion.new()
		ev.device = a[0]
		ev.set_meta(SLOT_META, a[0])
		ev.axis = a[1]
		ev.axis_value = 0.0
		Input.parse_input_event(ev)
		_held_axes.erase(id)


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
