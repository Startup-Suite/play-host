extends SceneTree
## Headless self-test for addons/suite_play (task 01a0db5f stage 3). Run on a
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

	print("SELFTEST %s (%d failures)" % ["PASS" if failures.is_empty() else "FAIL", failures.size()])
	quit(0 if failures.is_empty() else 1)
