package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

var Version = "dev"

func SetService(name string) {
	if name != "" {
		serviceName = name
	}
	if Version == "dev" {
		Version = computeVersion(serviceName)
	}
}

func ServiceName() string { return serviceName }

type versionState struct {
	Counter int              `json:"counter"`
	Seen    map[string]int64 `json:"seen"`
}

func versionStatePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for i := 1; i < cfg.BuildVersionUpLevels; i++ {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, ".build_version"), nil
}

func computeVersion(service string) string {
	exe, err := os.Executable()
	if err != nil {
		return "dev"
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "dev"
	}
	myMtime := info.ModTime().Unix()

	statePath, err := versionStatePath()
	if err != nil {
		return "dev"
	}

	st := versionState{Seen: map[string]int64{}}
	if raw, err := os.ReadFile(statePath); err == nil {
		if jerr := json.Unmarshal(raw, &st); jerr != nil {
			Error("computeVersion: unreadable version state %s, starting fresh: %v", statePath, jerr)
			st = versionState{Seen: map[string]int64{}}
		}
		if st.Seen == nil {
			st.Seen = map[string]int64{}
		}
	}

	if st.Seen[service] != myMtime {
		st.Counter++
		st.Seen[service] = myMtime
		if out, err := json.Marshal(st); err == nil {
			if err := os.WriteFile(statePath, out, 0660); err != nil {
				Error("computeVersion: failed to persist %s: %v", statePath, err)
			}
		}
	}

	return strconv.Itoa(st.Counter/10) + "." + strconv.Itoa(st.Counter%10)
}

func CurrentVersion() string { return Version }
