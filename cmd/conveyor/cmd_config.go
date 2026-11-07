package main

import (
	"fmt"
	"sort"

	"github.com/JayaSuryaOolio/conveyor/internal/core"
	"github.com/JayaSuryaOolio/conveyor/internal/store"
)

func init() { register("config", runConfig, "list, get or set settings") }

func runConfig(home string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: conveyor config list | get k | set k v")
	}
	return withService(home, func(s *core.Service) error {
		switch {
		case args[0] == "list" && len(args) == 1:
			m := store.ListSettings(s.DB)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(out, "%s=%s\n", k, m[k])
			}
			return nil
		case args[0] == "get" && len(args) == 2:
			fmt.Fprintln(out, store.GetSetting(s.DB, args[1]))
			return nil
		case args[0] == "set" && len(args) == 3:
			return s.SetSetting(args[1], args[2])
		}
		return fmt.Errorf("usage: conveyor config list | get k | set k v")
	})
}
