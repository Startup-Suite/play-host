# Stage 6 (01a0dbd6): a deliberate link blip. Kills the dev core's
# RuntimeSocket transport process for runtime:play-host-wave-dev (never any
# other runtime), which drops the host's websocket exactly as a network or
# framing fault would. Prints the pids it killed.
target = {Phoenix.Socket, PlatformWeb.RuntimeSocket, "runtime:play-host-wave-dev"}
pids = for p <- Process.list(), :proc_lib.get_label(p) == target, do: p
Enum.each(pids, &Process.exit(&1, :kill))
IO.puts("BLIP killed=#{inspect(pids)} at=#{DateTime.utc_now() |> DateTime.to_iso8601()}")
