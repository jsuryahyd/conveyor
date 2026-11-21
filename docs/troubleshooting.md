# Troubleshooting

Each recipe names a symptom, how to confirm it, and the fix. Prefer the fix that goes through Conveyor (`conveyor hook`, `conveyor task ...`) over editing `conveyor.db` by hand: those paths run the same parsing and status moves a normal run does. The quick checks (nothing starts, untrusted workspace, logs) are in the [guide](guide.md#troubleshooting).

To have an agent do it, run `claude "/conveyor-doctor fix CV-9"` (or `fix` with no key to scan every task) in the app's integrated terminal, or comment that request on any task that is not running. A comment on the stuck task itself only queues, because the task is still running.

When you find a new failure mode and a manual fix for it, add it here. When the dispatcher learns to fix it on its own, keep the recipe and say since when it is automatic.

## A run stays "running" after its session finished

Seen on CV-9 run 13. The session was resumed with `claude --bg --resume <id> "<prompt>"`. Claude Code woke it **without the options it was started with**: it got a new name, no `--settings <home>/claude-settings.json`, and so no `conveyor hook Stop`. The agent finished and printed its status block, but Conveyor never heard about it.

**Confirm**

```sh
conveyor task show KEY --runs                  # last run: running
conveyor task sessions KEY                     # its session id
claude agents --json --all                     # that sessionId: "state": "done", "status": "idle"
grep KEY ~/.conveyor/logs/hook.log             # no Stop for that session
```

**Fix.** Replay the Stop hook from the session's transcript. Conveyor then parses the status block, finishes the run and moves the task, exactly as if the hook had fired:

```sh
SID=<session id>
CWD=~/.conveyor/workspaces/KEY/<primary repo>   # the session's cwd from `claude agents --json --all`
T=$(ls ~/.claude/projects/*/$SID.jsonl)
python3 - "$SID" "$CWD" "$T" <<'EOF' | conveyor hook Stop
import json, sys
sid, cwd, path = sys.argv[1:]
last = ""
for line in open(path):
    e = json.loads(line)
    if e.get("type") == "assistant":
        for c in e["message"].get("content", []):
            if isinstance(c, dict) and c.get("type") == "text":
                last = c["text"]
print(json.dumps({"session_id": sid, "cwd": cwd, "transcript_path": path, "last_assistant_message": last}))
EOF
conveyor task show KEY --runs                  # run finished with its summary
```

**Prevented since CV-47.** Conveyor now passes `--settings <home>/claude-settings.json` on resume too. If a resumed run still never ends and `hook.log` has no `UserPromptSubmit`/`Stop` for its session (seen on CV-46 run 148, where the resume started "a copy as" a new session id), check that `<home>/claude-settings.json` exists and replay the Stop hook as above.

Before that, Conveyor did not detect this on its own: it is a Claude Code resume bug, and checking every session each tick costs more than the rare case is worth. The `conveyor-doctor` skill finds and fixes it on demand (see the top of this page).

## A resumed run is adopted by a new session id

Seen on CV-46 run 148 and CV-56 run 183; the architecture is in [session-identity.md](session-identity.md). `claude --bg --resume` started the work as a copy under a new session id. Hooks fire, but they name an id the resumed run does not have, so Conveyor creates an `interactive` run for them and the resumed run never gets its `Stop`: the task stays `running` after the agent finished.

**Confirm**

```sh
conveyor task show KEY --runs --json     # last auto/resume run: running, bg_id != first 8 chars of session_id
                                         # plus an interactive run whose session_id starts with that bg_id
claude agents --json --all               # that bg_id's sessionId is the NEW one; state done / status idle
grep KEY ~/.conveyor/logs/hook.log       # UserPromptSubmit/Stop for the new session id
```

**Fix.** Replay the Stop under the **resumed run's** session id (the old one, from `conveyor task show KEY --runs --json`), with the **new** session's transcript and last message. That makes the hook resolve the auto run, which finishes it and moves the task (to todo if a comment is queued, so the dispatcher answers it). Replaying under the new session id does not work: it resolves the interactive run, and interactive runs never move the task.

```sh
SID=<session_id of the stuck resume run>        # e.g. 106b7dbf-...
NEW=<sessionId of its bg agent>                 # claude agents --json --all, e.g. 4417568e-...
CWD=~/.conveyor/workspaces/KEY/<primary repo>
T=$(ls ~/.claude/projects/*/$NEW.jsonl)
python3 - "$SID" "$CWD" "$T" <<'EOF2' | conveyor hook Stop
# same script as the first recipe: print the JSON payload with session_id=$SID, transcript_path=$T,
# last_assistant_message = the last assistant text in $T
EOF2
conveyor task sync KEY
conveyor task show KEY --runs --json     # resume run finished, task moved
```

Run it when the session is idle if you can; a live session's own Stop only touches the interactive run, so replaying mid-turn is safe but moves the task before the turn ends. The interactive run stays `running` until its session leaves the agents list (the reaper finishes it).

**Prevented since CV-58.** The hook adopts the launching resume run and the reaper follows the bg agent's session id; see [session-identity.md](session-identity.md#prevention-since-cv-58). The recipe stays for runs stuck from before.

## Todo tasks wait with a free-looking board

Seen on CV-190. `max_parallel` counts every active auto run, and a run stayed active because its session was still listed by `claude agents --json --all` (`"state": "done"`, or still alive after the task was cancelled), so the reaper never saw it vanish. The dispatcher log says `max_parallel N reached` when todo tasks wait.

**Confirm**

```sh
conveyor task show KEY --runs     # active run on a cancelled / needs_you / done task
grep "max_parallel" ~/.conveyor/conveyor.log
```

**Fix.** Automatic since CV-190: the reaper fails an active run whose task is cancelled or done (and stops its session), or whose session is done/stopped while the task is not running. Before that, fail the run by hand in the DB.

## A task has a second PR that Conveyor doesn't track

Also seen on CV-9. After PR #90 merged, a comment reopened the task, and the agent opened #92 from a new branch (`cv/CV-9-readme-tone`). Conveyor follows one branch per repo (`cv/<KEY>-<slug>`), so it never saw #92 and the task won't finish when #92 merges.

**Fix.** Point the task at the PR it should follow:

```sh
conveyor task set KEY --repo <repo> --pr https://github.com/<owner>/<repo>/pull/92
conveyor task sync KEY
```

If that PR is already merged, the task can simply be closed: `conveyor task move KEY done`.

**Prevented since #95.** A comment on a task with a merged PR now starts a follow-up sub-task (`KEY` → `KEYA`) with its own branch, instead of reopening the task.

## A resume run stays "running" next to a second, finished run

Seen on CV-55 (runs 190/191 and 193/194). A task with a finished run gets a comment, so Conveyor starts an `auto/resume` run. Claude Code starts the resumed session under a **new session id** (the run's `bg_id`, not the `session_id` Conveyor stored). The hooks then report the new id, which matches no run, so Conveyor opens a second `interactive/fresh` run for it. That run gets the Stop hook and finishes, with the summary and status block. The resume run never hears anything, stays `running` with no activity, and holds the task `running`. The human sees the agent's answer under the other run, or not as a message at all.

Run 190 (`bg_id` 972c6af9) kept the old session `464739a3` while the work happened in `972c6af9`. Run 193 (`bg_id` 7c3e0439) kept `847bd280` while the work happened in `7c3e0439`.

**Confirm**

```sh
conveyor task show KEY --runs --json   # an auto/resume run: status running, calls=0, session_id != bg_id;
                                       # right after it, an interactive/fresh run: finished, same task, started 1-2s later
grep KEY ~/.conveyor/logs/hook.log     # UserPromptSubmit and Stop for the bg_id session, none for the stored session_id
claude agents --json --all             # the bg_id session: "state": "done"
```

This differs from the recipe above: there the Stop hook never fired. Here it fired, but finished the wrong run, so replaying it only finishes the interactive run again.

**Fix.** The work is already recorded on the interactive run. End the empty resume run with `conveyor task cancel KEY` (it moves the task to `cancelled`, stops its sessions and fails the run; the run turns `failed` a few seconds later). `cancelled -> review` is refused by `conveyor task move`, so reopen the task from the UI if it is not finished.

Last resort for a lost run, not a fix: move the task to `review` or `done` and confirm; that ends its leftover auto runs.

**Prevented since CV-58**, same fix as the recipe above. Moving a task to `review` or `done` with active auto runs asks first (UI modal, CLI `--stop`), then marks them failed ("stopped") and stops their sessions. Manual sessions are left alone.

## Every launch fails with "claude CLI not found"

Seen on CV-37B1 runs 316/317. Conveyor.app was opened from the Dock or Finder, which gives it `PATH=/usr/bin:/bin:/usr/sbin:/sbin`. The dispatcher runs a bare `claude` (and the hooks a bare `conveyor`), so no task can start, even though `claude` works in your terminal. The app was fine when started from a terminal.

**Confirm**

```sh
grep "app start" ~/.conveyor/conveyor.log | tail -1   # PATH entries=4 first=[/usr/bin /bin /usr/sbin /sbin]
grep -c "claude CLI not found" ~/.conveyor/logs/dispatcher.log
```

**Fix.** Quit Conveyor and relaunch it from a terminal so it inherits your PATH: `/Applications/Conveyor.app/Contents/MacOS/Conveyor`. Then re-queue the failed tasks with `conveyor task move KEY todo`.

**Prevented since this change.** `Startup` runs `$SHELL -ilc` and adds the login shell's PATH entries when `claude` is not on PATH (`internal/ui/shellenv.go`). If it still fails, check that `claude` is found in a fresh login shell (`zsh -ilc 'which claude'`).

## An Antigravity task finishes in the log but Conveyor marks it "needs_you"

Seen on CV-11A run 66. Antigravity's `Stop` lifecycle hook does not pass `last_assistant_message` on stdin. Conveyor's hook handler parsed an empty message, found no `<conveyor>` status block, set `final_message: ""` and moved the task to `needs_you`, while the full response and status block were present in `~/.conveyor/agy-logs/<KEY>.log`.

**Confirm**

```sh
conveyor task show KEY --runs --json     # last run: status finished, final_message: "", block_status: null
grep -c "<conveyor>" ~/.conveyor/agy-logs/KEY.log  # returns > 0
```

**Fix.** Replay the Stop hook using the session log:

```sh
KEY=<task key>
CWD=$(conveyor task show $KEY --json | python3 -c 'import sys, json; repos = json.load(sys.stdin).get("repos", []); print(repos[0]["worktree_path"] if repos else "")')
python3 - "$CWD" "$HOME/.conveyor/agy-logs/$KEY.log" <<'EOF' | conveyor hook Stop
import json, sys
cwd, log_path = sys.argv[1], sys.argv[2]
content = open(log_path, "r", encoding="utf-8").read()
print(json.dumps({"cwd": cwd, "last_assistant_message": content}))
EOF
conveyor task show KEY                   # run updated with summary, task moved to review
```

**Prevented since CV-16.** Conveyor's `hook.Handle` now reads `~/.conveyor/agy-logs/<KEY>.log` when `LastAssistantMessage` is empty on an `agy` session, and `resolveRun` adopts active `agy` auto runs.
