# 01a0fe45 stage 5 rig. Evaluated in the moon dev BEAM via rpc-01a0fe45.sh.
# R5_MODE:
#   fixture - (idempotent) general space, Survival Game project, dev task with
#             id 01a0fe15-e5c7-7001-acb7-83bc3d332ede (so its own branch is
#             task/01a0fe15-...) set in_review; runtime play-host-wave-dev-01a0fe45
#   token   - activate the runtime, print its token (piped straight to wave)
#   hosts   - connected play hosts and their declared features
#   start   - start a session through the REAL GameStream.start_session path;
#             R5_SHA is handed in through an inline runner ONLY if the default
#             runner cannot read the branch head (recorded as `via`)
#   canvas  - a canvas in general holding the current session's node
#   report / stop / row
import Ecto.Query
alias Platform.Repo

env = Map.new(~w(R5_MODE R5_SESSION R5_SHA), fn k -> {k, System.get_env(k)} end)
Enum.each(Map.keys(env), &System.delete_env/1)
mode = env["R5_MODE"] || "report"
rid = "play-host-wave-dev-01a0fe45"
task_id = "01a0fe15-e5c7-7001-acb7-83bc3d332ede"
repo_url = "https://github.com/ryanmilvenan/survival-game"
sid = fn -> env["R5_SESSION"] || :persistent_term.get(:gs_r5_session_01a0fe45, nil) end
jordan = fn -> Repo.one(from(u in Platform.Accounts.User, where: u.email == "jordan@localhost")) end

out =
  case mode do
    "fixture" ->
      space =
        Platform.Chat.get_space_by_slug("general") ||
          (fn ->
             {:ok, sp} = Platform.Chat.create_space(%{name: "general", slug: "general", kind: "channel"})
             sp
           end).()

      project =
        Repo.one(from(p in Platform.Tasks.Project, where: p.repo_url == ^repo_url, limit: 1)) ||
          (fn ->
             {:ok, p} =
               Platform.Tasks.create_project(%{
                 name: "Survival Game",
                 slug: "survival-game-01a0fe45",
                 repo_url: repo_url,
                 default_branch: "main"
               })

             p
           end).()

      unless Repo.get(Platform.Tasks.Task, task_id) do
        {:ok, t} =
          Platform.Tasks.create_task(%{
            title: "Survival Game review build (dev fixture, 01a0fe45 stage 5)",
            description: "Dev-DB fixture. Its id equals the real Survival Game task so its own branch is task/#{task_id}.",
            project_id: project.id,
            status: "backlog",
            priority: "low"
          })

        {1, _} = Repo.update_all(from(x in Platform.Tasks.Task, where: x.id == ^t.id), set: [id: task_id])
      end

      {1, _} = Repo.update_all(from(x in Platform.Tasks.Task, where: x.id == ^task_id), set: [status: "in_review"])

      owner = jordan.()

      runtime =
        owner &&
          (Platform.Federation.get_runtime_by_runtime_id(rid) ||
             (fn ->
                {:ok, r} =
                  Platform.Federation.register_runtime(owner.id, %{runtime_id: rid, display_name: "wave dev play host 01a0fe45"})

                r
              end).())

      t = Repo.get!(Platform.Tasks.Task, task_id)
      %{task_id: t.id, status: t.status, project_id: project.id, space_id: space.id, runtime: runtime && runtime.runtime_id, jordan: owner && owner.id}

    "token" ->
      {:ok, _r, token} = Platform.Federation.activate_runtime(Platform.Federation.get_runtime_by_runtime_id(rid))
      %{token: token}

    "hosts" ->
      %{available: Platform.GameStream.host_available?(), present: inspect(Platform.Federation.present_runtimes_with_feature("game_stream_host"), limit: :infinity)}

    "start" ->
      case Platform.GameStream.Session.session_for_host(rid) do
        nil -> :ok
        old -> Platform.GameStream.stop_session(old, "stage 5 rig reset")
      end

      Process.sleep(500)
      opts = [started_by: {:user, jordan.().id}, idle_timeout_s: 1800]
      first = Platform.GameStream.start_session(task_id, opts)

      {res, via} =
        case {first, env["R5_SHA"]} do
          {{:ok, _} = ok, _} ->
            {ok, "default runner"}

          {{:error, _} = err, sha} when is_binary(sha) and sha != "" ->
            :persistent_term.put(:gs_r5_sha_01a0fe45, sha)
            Code.compiler_options(ignore_module_conflict: true)

            Code.eval_string("""
            defmodule R5Runner01a0fe45 do
              def branch_head_sha(_), do: {:ok, :persistent_term.get(:gs_r5_sha_01a0fe45)}
            end
            """)

            {Platform.GameStream.start_session(task_id, [{:runner, :"Elixir.R5Runner01a0fe45"} | opts]),
             "inline runner (default runner said #{inspect(err)})"}

          {err, _} ->
            {err, "default runner"}
        end

      case res do
        {:ok, st} -> :persistent_term.put(:gs_r5_session_01a0fe45, st[:session_id] || st.session_id)
        _ -> :ok
      end

      %{result: inspect(res, limit: :infinity), via: via}

    "canvas" ->
      s = sid.()
      {:ok, st} = Platform.GameStream.status(s)
      space = Platform.Chat.get_space_by_slug("general")

      participant =
        Repo.one!(from(p in Platform.Chat.Participant, where: p.space_id == ^space.id and p.participant_id == ^jordan.().id, limit: 1))

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
                "title" => "Survival Game (01a0fe45 touch rig)",
                "latency_warn_ms" => 50
              }
            }
          ]
        }
      }

      {:ok, canvas, _msg} =
        Platform.Chat.create_canvas_with_message(space.id, participant.id, %{
          "title" => "Touch rig 01a0fe45 #{String.slice(s, -6, 6)}",
          "document" => doc
        })

      %{session_id: s, canvas_id: canvas.id}

    "report" ->
      s = sid.()
      %{session_id: s, status: s && inspect(Platform.GameStream.status(s), limit: :infinity), roster: s && inspect(Platform.GameStream.roster(s), limit: :infinity)}

    "stop" ->
      s = sid.()
      %{stopped: inspect(s && Platform.GameStream.stop_session(s, "stage 5 run over 01a0fe45"))}

    "row" ->
      s = sid.()
      r = Repo.get(Platform.GameStream.SessionRecord, s)
      r && Map.take(r, [:id, :task_id, :review_sha, :branch, :connection_path, :peak_players, :peak_spectators, :ended_state, :ended_reason, :started_at, :ended_at])
  end

IO.puts("R5_JSON " <> Jason.encode!(out))
