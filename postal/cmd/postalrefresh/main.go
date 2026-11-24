package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/CarlSinclair/csuit-libs/postal"
)

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func main() {
	dir := "postal/data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	client := &http.Client{Timeout: 3 * time.Minute}
	failed := 0
	changed := 0
	for _, c := range postal.Countries {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		fresh, err := postal.BuildFile(ctx, client, "csuit-libs-postal-refresh/1.0", c)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "postalrefresh: %s failed: %v\n", c, err)
			failed++
			continue
		}
		path := filepath.Join(dir, c+".txt.gz")
		if old, err := os.ReadFile(path); err == nil {
			a, errA := gunzip(old)
			b, errB := gunzip(fresh)
			if errA == nil && errB == nil && bytes.Equal(a, b) {
				fmt.Printf("postalrefresh: %s unchanged\n", c)
				continue
			}
		}
		if err := os.WriteFile(path, fresh, 0o664); err != nil {
			fmt.Fprintf(os.Stderr, "postalrefresh: writing %s failed: %v\n", path, err)
			failed++
			continue
		}
		fmt.Printf("postalrefresh: %s updated\n", c)
		changed++
	}
	fmt.Printf("postalrefresh: %d updated, %d failed\n", changed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
