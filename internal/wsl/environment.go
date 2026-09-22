package wsl

import (
	"os"
	"strings"
)

func cleanEnvironment() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "WSLENV") {
			names := []string{}
			for _, name := range strings.Split(value, ":") {
				variable, _, _ := strings.Cut(name, "/")
				if !strings.HasPrefix(strings.ToUpper(variable), "SCP_") {
					names = append(names, name)
				}
			}
			entry = key + "=" + strings.Join(names, ":")
		}
		if !strings.HasPrefix(strings.ToUpper(key), "SCP_") {
			env = append(env, entry)
		}
	}
	return env
}
