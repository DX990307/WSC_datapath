package runner

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var configFilesFlag = flag.String("config", "",
	"Comma-separated JSON configuration files (e.g. configs/hpca.json). Each "+
		"maps flag names to values; later files override earlier ones, and a "+
		"file may name a base file with \"extends\". Flags given explicitly on "+
		"the command line override every file.")
var configOutFlag = flag.String("config-out", "",
	"Write the effective value of every flag, and where it came from, as JSON "+
		"to this file. The \"flags\" object can be used as a -config file.")

var applyConfigOnce sync.Once

// configSource records where a flag's effective value came from.
type configSource struct {
	Value  string `json:"value"`
	Source string `json:"source"` // "default", a config file path, or "command line"
}

// applyConfigFiles sets flags from the -config files. It must run after
// flag.Parse and before any flag value is read; ParseFlag calls it first.
func applyConfigFiles() {
	applyConfigOnce.Do(func() {
		explicit := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

		sources := map[string]string{}
		var overridden []string
		for _, path := range splitConfigList(*configFilesFlag) {
			values, err := loadConfigFile(path, nil)
			if err != nil {
				panic(err)
			}
			for _, name := range sortedKeys(values) {
				if flag.Lookup(name) == nil {
					panic(fmt.Sprintf("config %s: unknown flag %q", path, name))
				}
				if name == "config" || name == "config-out" {
					panic(fmt.Sprintf("config %s: %q cannot be set in a config file", path, name))
				}
				if explicit[name] {
					overridden = append(overridden, name)
					continue
				}
				if err := flag.Set(name, values[name]); err != nil {
					panic(fmt.Sprintf("config %s: flag %q: %v", path, name, err))
				}
				sources[name] = path
			}
		}

		if *configFilesFlag != "" {
			fmt.Printf("[Config] config files: %s\n", *configFilesFlag)
		}
		for _, name := range uniqueSorted(overridden) {
			fmt.Printf("[Config] command line overrides config file: -%s=%s\n",
				name, flag.Lookup(name).Value.String())
		}
		if *configOutFlag != "" {
			writeEffectiveConfig(*configOutFlag, explicit, sources)
		}
	})
}

func splitConfigList(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadConfigFile returns flag values as strings. "extends" names a base file
// relative to this one; keys starting with "_" are comments.
func loadConfigFile(path string, seen []string) (map[string]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for _, s := range seen {
		if s == abs {
			return nil, fmt.Errorf("config %s: extends cycle", path)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	values := map[string]string{}
	if base, ok := raw["extends"]; ok {
		baseName, ok := base.(string)
		if !ok {
			return nil, fmt.Errorf("config %s: extends must be a file name", path)
		}
		basePath := baseName
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(filepath.Dir(path), basePath)
		}
		values, err = loadConfigFile(basePath, append(seen, abs))
		if err != nil {
			return nil, err
		}
	}
	for name, v := range raw {
		if name == "extends" || strings.HasPrefix(name, "_") {
			continue
		}
		s, err := configValueString(v)
		if err != nil {
			return nil, fmt.Errorf("config %s: flag %q: %w", path, name, err)
		}
		values[name] = s
	}
	return values, nil
}

func configValueString(v interface{}) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return fmt.Sprintf("%t", x), nil
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x)), nil
		}
		return fmt.Sprintf("%g", x), nil
	default:
		return "", fmt.Errorf("value must be a string, number, or boolean")
	}
}

func writeEffectiveConfig(path string, explicit map[string]bool, sources map[string]string) {
	all := map[string]configSource{}
	flagsOnly := map[string]string{}
	flag.VisitAll(func(f *flag.Flag) {
		if f.Name == "config" || f.Name == "config-out" {
			return
		}
		src := "default"
		if explicit[f.Name] {
			src = "command line"
		} else if p, ok := sources[f.Name]; ok {
			src = p
		}
		all[f.Name] = configSource{Value: f.Value.String(), Source: src}
		if src != "default" {
			flagsOnly[f.Name] = f.Value.String()
		}
	})
	out := map[string]interface{}{
		"config_files": splitConfigList(*configFilesFlag),
		"args":         os.Args[1:],
		"flags":        flagsOnly,
		"all_flags":    all,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func uniqueSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// lookupRunnerFlag returns the named flag if the runner package defines it.
func lookupRunnerFlag(name string) *flag.Flag {
	return flag.Lookup(name)
}
