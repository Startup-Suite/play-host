# 01a0dbd6 stage 5 measurement rig. Evaluated in the moon dev BEAM via
# s5rpc.sh. S5_MODE:
#   fixture  - (idempotent) Voltron project + one dev-DB task, set in_review
#   task     - print the fixture task (id, status, project repo)
#   start    - start a session for the fixture task through the REAL
#              GameStream.start_session path (ReviewBuild: in_review task,
#              project repo, task/<full uuid> branch). S5_SHA, when set, is
#              handed in through an inline runner ONLY if the default runner
#              cannot read the branch head (recorded in the output).
#              S5_RELAY=1 force_relay, S5_MAXP, S5_MAXS roster caps via the
#              surface control are NOT overridable here (control 4/4).
#   report   - status + roster of the current session
#   stop     - stop the current session
#   row      - the game_stream_sessions row for S5_SESSION (or current)
import Ecto.Query
alias Platform.Repo

# Env vars set by s5rpc.sh live in the dev node's OS env and would outlive
# this call; read them once and clear them so no later call inherits them.
env = Map.new(~w(S5_MODE S5_SESSION S5_SHA S5_RELAY), fn k -> {k, System.get_env(k)} end)
Enum.each(Map.keys(env), &System.delete_env/1)
mode = env["S5_MODE"] || "report"
title = "Voltron four-square multiplayer fixture (01a0dbd6 stage 5)"
sid = fn -> env["S5_SESSION"] || :persistent_term.get(:gs_s5_session_01a0dbd6, nil) end

find_task = fn ->
  Repo.one(
    from(t in Platform.Tasks.Task,
      where: t.title == ^title and is_nil(t.deleted_at),
      order_by: [desc: t.inserted_at],
      limit: 1
    )
  )
end

out =
  case mode do
    "fixture" ->
      project =
        Repo.one(
          from(p in Platform.Tasks.Project,
            where: p.repo_url == "https://github.com/ryanmilvenan/voltron",
            limit: 1
          )
        ) ||
          (fn ->
             {:ok, p} =
               Platform.Tasks.create_project(%{
                 name: "Voltron",
                 slug: "voltron-01a0dbd6",
                 repo_url: "https://github.com/ryanmilvenan/voltron",
                 default_branch: "main"
               })

             p
           end).()

      task =
        find_task.() ||
          (fn ->
             {:ok, t} =
               Platform.Tasks.create_task(%{
                 title: title,
                 description:
                   "Test fixture for Suite task 01a0dbd6 (multiplayer game_stream). " <>
                     "A CanvasLayer in res://main/main.tscn with four squares, one per " <>
                     "input device 0-3. Delete the voltron branch after the task-level review.",
                 project_id: project.id,
                 status: "backlog",
                 priority: "low"
               })

             t
           end).()

      # A dev-DB fixture: put it straight in review (no plan engine here).
      {1, _} =
        Repo.update_all(from(t in Platform.Tasks.Task, where: t.id == ^task.id),
          set: [status: "in_review"]
        )

      t = Repo.get!(Platform.Tasks.Task, task.id)
      %{task_id: t.id, status: t.status, project_id: project.id, repo_url: project.repo_url}

    "task" ->
      t = find_task.()
      %{task_id: t && t.id, status: t && t.status}

    "start" ->
      rid = "play-host-wave-dev"

      case Platform.GameStream.Session.session_for_host(rid) do
        nil -> :ok
        old -> Platform.GameStream.stop_session(old, "stage 5 rig reset")
      end

      Process.sleep(500)
      t = find_task.()
      jordan = Repo.one!(from(u in Platform.Accounts.User, where: u.email == "jordan@localhost"))

      opts = [
        started_by: {:user, jordan.id},
        idle_timeout_s: 1800,
        force_relay: env["S5_RELAY"] == "1"
      ]

      first = Platform.GameStream.start_session(t.id, opts)

      {res, via} =
        case {first, env["S5_SHA"]} do
          {{:ok, _} = ok, _} ->
            {ok, "default runner"}

          {{:error, _} = err, sha} when is_binary(sha) and sha != "" ->
            mod = :"Elixir.S5Runner01a0dbd6"
            :persistent_term.put(:gs_s5_sha_01a0dbd6, sha)

            # Always (re)define: an older definition may still be loaded.
            Code.compiler_options(ignore_module_conflict: true)

            Code.eval_string("""
            defmodule S5Runner01a0dbd6 do
              def branch_head_sha(_), do: {:ok, :persistent_term.get(:gs_s5_sha_01a0dbd6)}
            end
            """)

            {Platform.GameStream.start_session(t.id, [{:runner, mod} | opts]),
             "inline runner (default runner said #{inspect(err)})"}

          {err, _} ->
            {err, "default runner"}
        end

      case res do
        {:ok, st} ->
          :persistent_term.put(:gs_s5_session_01a0dbd6, st[:session_id] || st.session_id)

        _ ->
          :ok
      end

      %{result: inspect(res, limit: :infinity), via: via, task_id: t.id}

    "canvas" ->
      # A canvas in general holding the current session's node (the
      # play_session_start canvas lives in the task's execution space; this
      # one is just where Dev users already participate).
      s = sid.()
      {:ok, st} = Platform.GameStream.status(s)
      jordan = Repo.one!(from(u in Platform.Accounts.User, where: u.email == "jordan@localhost"))
      space = Platform.Chat.get_space_by_slug("general")

      participant =
        Repo.one!(
          from(p in Platform.Chat.Participant,
            where: p.space_id == ^space.id and p.participant_id == ^jordan.id,
            limit: 1
          )
        )

      doc = %{
        "version" => 1,
        "revision" => 1,
        "root" => %{
          "id" => "root",
          "type" => "stack",
          "props" => %{"gap" => 12},
          "children" => [
            %{
              "id" => "game-stream",
              "type" => "game_stream",
              "props" => %{
                "session_id" => s,
                "review_sha" => st[:review_sha],
                "branch" => st[:branch],
                "title" => "Voltron: four squares",
                "latency_warn_ms" => 50
              }
            }
          ]
        }
      }

      {:ok, canvas, _msg} =
        Platform.Chat.create_canvas_with_message(space.id, participant.id, %{
          "title" => "Measure 01a0dbd6 #{String.slice(s, -6, 6)}",
          "document" => doc
        })

      %{session_id: s, canvas_id: canvas.id, space_id: space.id}

    "report" ->
      s = sid.()

      %{
        session_id: s,
        status: s && inspect(Platform.GameStream.status(s), limit: :infinity),
        roster: s && inspect(Platform.GameStream.roster(s), limit: :infinity)
      }

    "stop" ->
      s = sid.()
      %{stopped: inspect(s && Platform.GameStream.stop_session(s, "stage 5 run over 01a0dbd6"))}

    "row" ->
      s = sid.()
      r = Repo.get(Platform.GameStream.SessionRecord, s)

      r &&
        Map.take(r, [
          :id,
          :task_id,
          :review_sha,
          :branch,
          :connection_path,
          :rtt_p50_ms,
          :rtt_p95_ms,
          :fps_avg,
          :slot_stats,
          :peak_players,
          :peak_spectators,
          :ended_state,
          :ended_reason,
          :started_at,
          :ended_at
        ])
  end

IO.puts("S5_JSON " <> Jason.encode!(out))
