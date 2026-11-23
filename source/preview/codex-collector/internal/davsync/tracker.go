package davsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	pathpkg "path"
	"regexp"
	"slices"
)

var trackerFolder = regexp.MustCompile(`^(controls|enrollments|[0-9a-f]{32}/[0-9]{4})$`)
var trackerName = regexp.MustCompile(`^(policy\.magpie-quotas|prices\.tclash|[0-9a-f]{32}-[0-9a-f]{32}\.magpie-usage|[0-9]{4}-[0-9]{2}-[0-9]{2}\.magpie-usage)$`)
var trackerDevice = regexp.MustCompile(`^[0-9a-f]{32}$`)
var trackerYear = regexp.MustCompile(`^[0-9]{4}$`)
var trackerDay = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}\.magpie-usage$`)
var trackerEnrollment = regexp.MustCompile(`^[0-9a-f]{32}-[0-9a-f]{32}\.magpie-usage$`)

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

func (r *UsageRepository) directories(ctx context.Context, path string, allowed *regexp.Regexp) ([]string, error) {
	g := r.remote
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	res, err := g.send(ctx, http.MethodGet, "contents/"+path, nil, "application/vnd.github+json", g.branch)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, githubError(res)
	}
	var entries []githubContent
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&entries); err != nil {
		return nil, err
	}
	if len(entries) >= 1000 {
		return nil, errors.New("the device folder reached GitHub's listing limit")
	}
	var names []string
	for _, entry := range entries {
		if entry.Type == "dir" && allowed.MatchString(entry.Name) {
			names = append(names, entry.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func (r *UsageRepository) DeviceIDs(ctx context.Context) ([]string, error) {
	return r.directories(ctx, r.remote.prefix, trackerDevice)
}

func (r *UsageRepository) DeviceYears(ctx context.Context, device string) ([]string, error) {
	if !trackerDevice.MatchString(device) {
		return nil, errors.New("invalid usage device")
	}
	return r.directories(ctx, pathpkg.Join(r.remote.prefix, device), trackerYear)
}

func (r *UsageRepository) DeleteDeviceFile(ctx context.Context, folder, name string) error {
	if (folder == "enrollments" && !trackerEnrollment.MatchString(name)) ||
		(folder != "enrollments" && (!trackerDay.MatchString(name) || len(folder) < 4 || folder[len(folder)-4:] != name[:4])) {
		return errors.New("invalid device usage file")
	}
	g, err := r.scoped(folder)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		err = g.remove(ctx, name)
		if !errors.Is(err, errChanged) {
			return err
		}
	}
	return err
}
