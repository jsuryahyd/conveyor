package main

import (
	"flag"
	"fmt"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/runner"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func init() { register("detect", runDetect, "detect installed CLI agent runtimes") }

func runDetect(home string, args []string) error {
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "apply detected runner as default_runner in settings if current is not installed")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	agents := runner.DetectRunners()

	return withService(home, func(s *core.Service) error {
		cur := store.GetSetting(s.DB, "default_runner")
		var changed bool
		if *apply {
			var err error
			cur, changed, err = runner.AutoConfigureDefaultRunner(s.DB)
			if err != nil {
				return err
			}
		}

		if *jsonOut {
			type detectResult struct {
				CurrentRunner string            `json:"current_runner"`
				Applied       bool              `json:"applied"`
				Agents        []runner.CLIAgent `json:"agents"`
			}
			return printJSON(detectResult{
				CurrentRunner: cur,
				Applied:       changed,
				Agents:        agents,
			})
		}

		fmt.Fprintln(out, "Detected agent runtimes:")
		for _, a := range agents {
			status := "[not found]"
			if a.Installed {
				status = fmt.Sprintf("[detected: %s]", a.Path)
			}
			marker := " "
			if a.ID == cur {
				marker = "*"
			}
			fmt.Fprintf(out, " %s %-12s %-8s %s\n", marker, a.Name, "("+a.Command+")", status)
		}
		fmt.Fprintf(out, "\nDefault runner: %s\n", cur)
		if changed {
			fmt.Fprintln(out, "(Updated default_runner to detected runtime)")
		}
		return nil
	})
}
