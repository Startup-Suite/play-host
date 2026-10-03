# READ-ONLY: play-host-wave presence, features and live session on this core.
hosts = Platform.Federation.present_runtimes_with_feature("game_stream_host")
for h <- hosts do
  f = Platform.Federation.declared_features(h.metadata)
  build = get_in(h.metadata || %{}, ["client_info", "build"]) || get_in(h.metadata || %{}, ["client_info", "version"])
  IO.puts("HOST #{h.runtime_id} features=#{inspect(f)} version=#{inspect(build)} session=#{inspect(Platform.GameStream.Session.session_for_host(h.runtime_id))}")
end
IO.puts("present_hosts=#{length(hosts)} wave_present=#{Enum.any?(hosts, &(&1.runtime_id == "play-host-wave"))}")
IO.puts("WAVE_SESSION=#{inspect(Platform.GameStream.Session.session_for_host("play-host-wave"))}")
IO.puts("live_sessions=#{DynamicSupervisor.count_children(Platform.GameStream.Supervisor.sessions()).active}")
