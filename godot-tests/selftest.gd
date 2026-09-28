extends SceneTree
## Headless self-test for addons/suite_play (task 01a0db5f stage 3; two
## player slots added by task 01a0dbd6 stage 3). Run on a
## Godot 4.7 install from a scratch project holding the addon:
##   godot --headless --path <dir> --script res://selftest.gd -- --keynames=<keynames.json>
## keynames.json is `play-host keynames` output: every Godot key name the
## host's KeyboardEvent.code map can emit. Exits 0 on pass, 1 on failure.
##
## It measures the plan's RISK directly: does Input.parse_input_event update
## the RAW state (Input.is_physical_key_pressed, Input.get_joy_axis,
## Input.is_joy_button_pressed), not only InputMap actions?

var failures: Array[String] = []


func _check(ok: bool, what: String) -> void:
	print(("PASS " if ok else "FAIL ") + what)
	if not ok:
		failures.append(what)


func _initialize() -> void:
	var sp: Node = load("res://addons/suite_play/suite_play.gd").new()
	root.add_child(sp)

	var names_path := ""
	for a in OS.get_cmdline_user_args():
		if a.begins_with("--keynames="):
			names_path = a.get_slice("=", 1)
	if names_path != "":
		var names = JSON.parse_string(FileAccess.get_file_as_string(names_path))
		var bad: Array = []
		for n in names:
			if sp.keycode_for(str(n)) == KEY_NONE:
				bad.append(n)
		_check(bad.is_empty(), "all %d host key names resolve via OS.find_keycode_from_string (unresolved: %s)" % [names.size(), bad])

	sp.handle_line('{"t":"key","k":"W","loc":0,"p":true,"e":false}')
	sp.handle_line('{"t":"key","k":"Shift","loc":2,"p":true,"e":false}')
	sp.handle_line('{"t":"jb","d":0,"b":0,"p":true,"v":1}')
	sp.handle_line('{"t":"ja","d":0,"a":1,"v":-0.75}')
	sp.handle_line('{"t":"ja","d":0,"a":5,"v":0.5}')
	Input.flush_buffered_events()
	_check(Input.is_physical_key_pressed(KEY_W), "key W -> Input.is_physical_key_pressed(KEY_W)")
	_check(Input.is_key_pressed(KEY_W), "key W -> Input.is_key_pressed(KEY_W)")
	_check(Input.is_physical_key_pressed(KEY_SHIFT), "right shift -> Input.is_physical_key_pressed(KEY_SHIFT)")
	_check(Input.is_joy_button_pressed(0, JOY_BUTTON_A), "jb A -> Input.is_joy_button_pressed(0, A)")
	_check(is_equal_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_Y), -0.75), "ja left_y -> Input.get_joy_axis = %.3f" % Input.get_joy_axis(0, JOY_AXIS_LEFT_Y))
	_check(is_equal_approx(Input.get_joy_axis(0, JOY_AXIS_TRIGGER_RIGHT), 0.5), "ja trigger_right -> Input.get_joy_axis = %.3f" % Input.get_joy_axis(0, JOY_AXIS_TRIGGER_RIGHT))
	_check(Input.is_action_pressed("ui_accept") == false, "ui_accept not pressed before Enter")
	sp.handle_line('{"t":"key","k":"Enter","loc":0,"p":true,"e":false}')
	Input.flush_buffered_events()
	_check(Input.is_action_pressed("ui_accept"), "key Enter -> InputMap ui_accept (Voltron main.gd:20 reads this)")
	# Report, not assert: a pad driven only through parse_input_event is not a
	# registered joypad, so a game that enumerates pads would not see it.
	print("INFO Input.get_connected_joypads() = %s" % [Input.get_connected_joypads()])

	sp.handle_line('{"t":"release_all"}')
	Input.flush_buffered_events()
	_check(not Input.is_physical_key_pressed(KEY_W) and not Input.is_physical_key_pressed(KEY_ENTER), "release_all releases keys")
	_check(not Input.is_joy_button_pressed(0, JOY_BUTTON_A), "release_all releases buttons")
	_check(is_zero_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_Y)), "release_all zeroes axes")

	_two_devices(sp)


## Task 01a0dbd6: two player slots. The host sends d = slot on every line.
## A game tells two keyboard players apart only in _input (key polling is
## global), and two pads by device in get_joy_axis / is_joy_button_pressed.
## Measured on Godot 4.7.2: events parsed during _initialize never reach
## _input (the tree is not processing yet), so the lines are sent on the
## first frame and read two frames later.
var _rec: Node
var _sp: Node
var _frames := 0
var _kbd := 0


func _two_devices(sp: Node) -> void:
	_sp = sp
	_kbd = InputEventKey.new().device
	print("INFO InputEventKey default device = %d" % _kbd)
	var gd := GDScript.new()
	gd.source_code = "extends Node\nvar got: Array = []\nfunc _input(e: InputEvent) -> void:\n\tif e is InputEventKey or e is InputEventJoypadButton or e is InputEventJoypadMotion:\n\t\tgot.append([e.get_class(), e.device, e.get_meta(&\"suite_play_slot\", -1), e.is_action(&\"ui_accept\")])\n"
	gd.reload()
	_rec = gd.new()
	root.add_child(_rec)


func _process(_delta: float) -> bool:
	_frames += 1
	if _frames == 1:
		_sp.handle_line('{"t":"key","d":0,"k":"D","loc":0,"p":true,"e":false}')
		_sp.handle_line('{"t":"key","d":1,"k":"W","loc":0,"p":true,"e":false}')
		_sp.handle_line('{"t":"key","d":0,"k":"Enter","loc":0,"p":true,"e":false}')
		_sp.handle_line('{"t":"key","d":1,"k":"Enter","loc":0,"p":true,"e":false}')
		_sp.handle_line('{"t":"jb","d":0,"b":0,"p":true,"v":1}')
		_sp.handle_line('{"t":"jb","d":1,"b":1,"p":true,"v":1}')
		_sp.handle_line('{"t":"ja","d":0,"a":0,"v":0.5}')
		_sp.handle_line('{"t":"ja","d":1,"a":1,"v":-0.5}')
		return false
	if _frames < 4:
		return false
	var got: Array = _rec.got
	print("INFO _input saw [class, device, meta slot, is ui_accept]: %s" % [got])
	var seen := {}
	for g in got:
		seen["%s:%d:%d" % [g[0], g[1], g[2]]] = true
	_check(seen.has("InputEventKey:%d:0" % _kbd) and seen.has("InputEventKey:%d:1" % (_kbd + 1)), "keys from slots 0 and 1 reach _input on devices %d and %d, meta slot 0 and 1" % [_kbd, _kbd + 1])
	_check(seen.has("InputEventJoypadButton:0:0") and seen.has("InputEventJoypadButton:1:1"), "pad buttons from slots 0 and 1 reach _input with event.device 0 and 1")
	_check(seen.has("InputEventJoypadMotion:0:0") and seen.has("InputEventJoypadMotion:1:1"), "pad axes from slots 0 and 1 reach _input with event.device 0 and 1")
	var enter0 := false
	var enter1 := true
	for g in got:
		if g[0] == "InputEventKey" and g[3]:
			if g[2] == 0:
				enter0 = true
		if g[0] == "InputEventKey" and g[2] == 1 and g[3]:
			enter1 = false
	_check(enter0, "slot 0's Enter is ui_accept (the built-in action is bound to the default key device)")
	print("INFO slot 1's Enter matched ui_accept: %s (built-in ui_* are bound to device %d; an All Devices action matches every slot)" % [not enter1, _kbd])
	_check(is_equal_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_X), 0.5) and is_equal_approx(Input.get_joy_axis(1, JOY_AXIS_LEFT_Y), -0.5) and is_zero_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_Y)), "pad axes are per device when polled (get_joy_axis(d, ...))")
	_check(Input.is_joy_button_pressed(0, JOY_BUTTON_A) and not Input.is_joy_button_pressed(0, JOY_BUTTON_B) and Input.is_joy_button_pressed(1, JOY_BUTTON_B), "pad buttons are per device when polled")
	# Releasing slot 1 leaves slot 0 held.
	_sp.handle_line('{"t":"release","d":1}')
	Input.flush_buffered_events()
	_check(Input.is_joy_button_pressed(0, JOY_BUTTON_A) and not Input.is_joy_button_pressed(1, JOY_BUTTON_B), "release d:1 releases only device 1's buttons")
	_check(is_equal_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_X), 0.5) and is_zero_approx(Input.get_joy_axis(1, JOY_AXIS_LEFT_Y)), "release d:1 zeroes only device 1's axes")
	_check(Input.is_physical_key_pressed(KEY_D) and not Input.is_physical_key_pressed(KEY_W), "release d:1 lifts slot 1's W and keeps slot 0's D")
	# Recorded, not asserted: key polling has no device, so if both slots hold
	# the same key, releasing one lifts it for both.
	_sp.handle_line('{"t":"key","d":1,"k":"D","loc":0,"p":true,"e":false}')
	_sp.handle_line('{"t":"release","d":1}')
	Input.flush_buffered_events()
	print("INFO slot 0 and slot 1 both held D, slot 1 released: Input.is_physical_key_pressed(KEY_D) = %s (polling is global)" % Input.is_physical_key_pressed(KEY_D))
	_sp.handle_line('{"t":"release_all"}')
	Input.flush_buffered_events()
	_check(not Input.is_joy_button_pressed(0, JOY_BUTTON_A) and is_zero_approx(Input.get_joy_axis(0, JOY_AXIS_LEFT_X)) and not Input.is_physical_key_pressed(KEY_D), "release_all still releases every device")

	print("SELFTEST %s (%d failures)" % ["PASS" if failures.is_empty() else "FAIL", failures.size()])
	quit(0 if failures.is_empty() else 1)
	return true
