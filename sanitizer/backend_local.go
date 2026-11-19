package sanitizer

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

//go:embed encoder/encode.sh
var encodeScript []byte

// EncodeScript returns the worker script, so an encoder repository can be kept
// identical to what this package runs locally.
func EncodeScript() []byte { return append([]byte(nil), encodeScript...) }

// LocalBackend runs the worker script on this host: one job at a time, at the
// lowest CPU priority, and only while the load average is below MaxLoad.
type LocalBackend struct {
	MaxLoad float64
	Threads int
	slot    chan struct{}
}

func NewLocalBackend(maxLoad float64, threads int) *LocalBackend {
	if maxLoad <= 0 {
		maxLoad = 10
	}
	if threads <= 0 {
		threads = 1
	}
	return &LocalBackend{MaxLoad: maxLoad, Threads: threads, slot: make(chan struct{}, 1)}
}

func (b *LocalBackend) Name() string { return "local" }

func loadAverage() (float64, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(raw))
	if len(f) == 0 {
		return 0, fmt.Errorf("empty /proc/loadavg")
	}
	return strconv.ParseFloat(f[0], 64)
}

func (b *LocalBackend) Submit(ctx context.Context, j Job) error {
	if err := validateJob(j); err != nil {
		return err
	}
	if err := CheckTools("ffmpeg", "ffprobe", "curl", "bash"); err != nil {
		return err
	}
	go b.run(j)
	return nil
}

func (b *LocalBackend) run(j Job) {
	b.slot <- struct{}{}
	defer func() { <-b.slot }()
	for {
		l, err := loadAverage()
		if err != nil {
			logger.Warn("video local backend: cannot read the load average, starting job %s anyway: %v", j.ID, err)
			break
		}
		if l < b.MaxLoad {
			break
		}
		logger.Info("video local backend: job %s waits, load %.1f is above %.1f", j.ID, l, b.MaxLoad)
		time.Sleep(time.Minute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-s")
	cmd.Stdin = strings.NewReader(string(encodeScript))
	cmd.Env = append(os.Environ(), jobEnv(j)...)
	cmd.Env = append(cmd.Env, "LOWER_PRIORITY=1", "THREADS="+strconv.Itoa(b.Threads))
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("video local backend: job %s worker exited with %v: %s", j.ID, err, strings.TrimSpace(string(out)))
		return
	}
	logger.Info("video local backend: job %s worker finished", j.ID)
}
