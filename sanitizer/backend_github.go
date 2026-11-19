package sanitizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/CarlSinclair/csuit-libs/logger"
)

// GitHubBackend starts the worker as a GitHub Actions workflow_dispatch run in
// a dedicated repository. Token needs only Actions write on that repository.
type GitHubBackend struct {
	Owner, Repo, Workflow, Ref, Token string
	Client                            *http.Client
}

func (b *GitHubBackend) Name() string { return "github" }

func (b *GitHubBackend) Submit(ctx context.Context, j Job) error {
	if err := validateJob(j); err != nil {
		return err
	}
	if b.Owner == "" || b.Repo == "" || b.Workflow == "" || b.Token == "" {
		return fmt.Errorf("github backend is not fully configured")
	}
	ref := b.Ref
	if ref == "" {
		ref = "main"
	}
	body, err := json.Marshal(map[string]any{
		"ref": ref,
		"inputs": map[string]string{
			"job_id":       j.ID,
			"source_url":   j.SourceURL,
			"upload_base":  j.UploadBase,
			"callback_url": j.CallbackURL,
			"token":        j.Token,
			"max_seconds":  strconv.Itoa(j.MaxSeconds),
			"mode":         modeOf(j),
		},
	})
	if err != nil {
		return fmt.Errorf("encode dispatch body: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/workflows/%s/dispatches", b.Owner, b.Repo, b.Workflow)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	c := b.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		logger.Error("video github backend: dispatch of job %s failed: %v", j.ID, err)
		return fmt.Errorf("dispatch workflow: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		logger.Error("video github backend: dispatch of job %s returned %d: %s", j.ID, resp.StatusCode, msg)
		return fmt.Errorf("github dispatch returned %d", resp.StatusCode)
	}
	logger.Info("video github backend: job %s dispatched to %s/%s", j.ID, b.Owner, b.Repo)
	return nil
}
