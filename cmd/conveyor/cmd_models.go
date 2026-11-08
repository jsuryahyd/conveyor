package main

import (
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
)

func init() {
	register("models", runModels, "Model catalog for runners (claude, agy); view or configure models")
}

func runModels(home string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "set":
			return runModelsSet(home, args[1:])
		case "add":
			return runModelsAdd(home, args[1:])
		case "reset":
			return runModelsReset(home, args[1:])
		}
	}

	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	runner := fs.String("runner", "", "Runner backend (claude, agy; default: from conveyor settings)")
	fs.StringVar(runner, "r", "", "Short alias for --runner")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 && *runner == "" {
		*runner = pos[0]
	}

	return withService(home, func(s *core.Service) error {
		ml := s.RunnerModels(*runner)
		if *asJSON {
			return printJSON(ml)
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		hasNames := false
		for _, m := range ml.Models {
			if m.Name != "" && m.Name != m.ID {
				hasNames = true
				break
			}
		}
		if hasNames {
			fmt.Fprintln(tw, "ID\tTIER\tALIAS\tNAME")
		} else {
			fmt.Fprintln(tw, "ID\tTIER\tALIAS")
		}
		for _, m := range ml.Models {
			alias := ""
			if m.Latest {
				alias = m.Tier
			}
			if hasNames {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Tier, alias, m.Name)
			} else {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", m.ID, m.Tier, alias)
			}
		}
		fmt.Fprintf(tw, "(source: %s)\n", ml.Source)
		return tw.Flush()
	})
}

func runModelsSet(home string, args []string) error {
	fs := flag.NewFlagSet("models set", flag.ContinueOnError)
	runner := fs.String("runner", "", "Runner backend (claude, agy; default: from conveyor settings)")
	fs.StringVar(runner, "r", "", "Short alias for --runner")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: conveyor models set <model...> [--runner agy|claude]")
	}
	return withService(home, func(s *core.Service) error {
		if err := s.SetRunnerModels(*runner, pos); err != nil {
			return err
		}
		r := *runner
		if r == "" {
			r = "default"
		}
		fmt.Fprintf(out, "updated %s models (%d configured)\n", r, len(pos))
		return nil
	})
}

func runModelsAdd(home string, args []string) error {
	fs := flag.NewFlagSet("models add", flag.ContinueOnError)
	runner := fs.String("runner", "", "Runner backend (claude, agy; default: from conveyor settings)")
	fs.StringVar(runner, "r", "", "Short alias for --runner")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("usage: conveyor models add <model> [--runner agy|claude]")
	}
	return withService(home, func(s *core.Service) error {
		cur := s.RunnerModels(*runner)
		var existing []string
		for _, m := range cur.Models {
			existing = append(existing, m.ID)
		}
		for _, p := range pos {
			if !slicesContains(existing, p) {
				existing = append(existing, p)
			}
		}
		if err := s.SetRunnerModels(*runner, existing); err != nil {
			return err
		}
		r := *runner
		if r == "" {
			r = "default"
		}
		fmt.Fprintf(out, "updated %s models (%d configured)\n", r, len(existing))
		return nil
	})
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func runModelsReset(home string, args []string) error {
	fs := flag.NewFlagSet("models reset", flag.ContinueOnError)
	runner := fs.String("runner", "", "Runner backend (claude, agy; default: from conveyor settings)")
	fs.StringVar(runner, "r", "", "Short alias for --runner")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	return withService(home, func(s *core.Service) error {
		if err := s.ResetRunnerModels(*runner); err != nil {
			return err
		}
		r := *runner
		if r == "" {
			r = "default"
		}
		fmt.Fprintf(out, "reset %s models to default detection\n", r)
		return nil
	})
}
