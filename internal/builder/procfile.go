package builder

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func ParseProcfile(dir string) (map[string]string, error) {
	path := filepath.Join(dir, "Procfile")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{"web": ""}, nil
		}
		return nil, err
	}
	defer f.Close()

	procs := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		procType := strings.TrimSpace(parts[0])
		command := strings.TrimSpace(parts[1])
		if procType != "" && command != "" {
			procs[procType] = command
		}
	}

	if len(procs) == 0 {
		return map[string]string{"web": ""}, nil
	}
	return procs, scanner.Err()
}
