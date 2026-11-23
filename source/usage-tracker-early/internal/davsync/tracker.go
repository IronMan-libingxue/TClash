package davsync

import (
	"context"
	"errors"
	"io/fs"
	pathpkg "path"
	"regexp"
)

var trackerFolder = regexp.MustCompile(`^(controls|enrollments|[0-9a-f]{32}/[0-9]{4})$`)
var trackerName = regexp.MustCompile(`^(policy\.magpie-quotas|[0-9a-f]{32}-[0-9a-f]{32}\.magpie-usage|[0-9]{4}-[0-9]{2}-[0-9]{2}\.magpie-usage)$`)

// UsageRepository provides private GitHub file transport without invoking
// the Magpie settings backup or restore workflow.
type UsageRepository struct {
	remote *github
}

func OpenUsageRepository(ctx context.Context, repo, token string) (*UsageRepository, error) {
	g, err := newGitHub(Config{URL: "github://" + repo + "/codex-usage-tracker/v1", Password: token})
	if err != nil {
		return nil, err
	}
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	return &UsageRepository{remote: g}, nil
}

func (r *UsageRepository) scoped(folder string) (*github, error) {
	if !trackerFolder.MatchString(folder) {
		return nil, errors.New("invalid usage storage folder")
	}
	g := *r.remote
	g.prefix = pathpkg.Join(g.prefix, folder)
	return &g, nil
}

func (r *UsageRepository) List(ctx context.Context, folder string) (map[string]string, error) {
	g, err := r.scoped(folder)
	if err != nil {
		return nil, err
	}
	return g.list(ctx)
}

func (r *UsageRepository) Read(ctx context.Context, folder, name string) ([]byte, error) {
	g, err := r.scoped(folder)
	if err != nil {
		return nil, err
	}
	if !trackerName.MatchString(name) {
		return nil, errors.New("invalid usage storage file name")
	}
	content, err := g.content(ctx, pathpkg.Join(usageFolder, name))
	if err != nil {
		return nil, err
	}
	if content == nil {
		return nil, fs.ErrNotExist
	}
	return g.bytes(ctx, content)
}

func (r *UsageRepository) Write(ctx context.Context, folder, name string, data []byte) error {
	g, err := r.scoped(folder)
	if err != nil {
		return err
	}
	if !trackerName.MatchString(name) {
		return errors.New("invalid usage storage file name")
	}
	for attempt := 0; attempt < 3; attempt++ {
		err = g.write(ctx, name, data)
		if !errors.Is(err, errChanged) {
			return err
		}
	}
	return err
}
