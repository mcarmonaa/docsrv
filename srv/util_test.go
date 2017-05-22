package srv

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func assertJSON(t *testing.T, handler http.Handler, requestURL string, expected interface{}) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", requestURL, nil)
	require.NoError(t, err, "unexpected error creating request")

	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	expectedJSON, err := json.Marshal(expected)
	require.NoError(t, err, "unexpected error marshaling json")
	require.Equal(t, string(expectedJSON), w.Body.String())
}

const expectedDocsOutput = `%s
/etc/shared
`

func assertMakefileOutput(t *testing.T, tmpDir, baseURL string) {
	fp := filepath.Join(tmpDir, "out")
	data, err := ioutil.ReadFile(fp)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf(expectedDocsOutput, baseURL), string(data))
}

func assertNotFound(t *testing.T, handler http.Handler, requestURL string) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", requestURL, nil)
	require.NoError(t, err, "unexpected error creating request")

	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code, "expected a not found")
}

func assertInternalError(t *testing.T, handler http.Handler, requestURL string) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", requestURL, nil)
	require.NoError(t, err, "unexpected error creating request")

	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code, "expected a not found")
}

func assertRedirect(t *testing.T, handler http.Handler, requestURL, expected string) {
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", requestURL, nil)
	require.NoError(t, err, "unexpected error creating request")

	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusTemporaryRedirect, w.Code, "expected a redirect")
	url := w.Header().Get("Location")
	require.Equal(t, expected, url, "wrong redirect url")
}

type gitHubMock struct {
	releases map[string]map[string]string
}

func newGitHubMock() *gitHubMock {
	return &gitHubMock{make(map[string]map[string]string)}
}

func (m *gitHubMock) add(project, version, url string) {
	if _, ok := m.releases[project]; !ok {
		m.releases[project] = make(map[string]string)
	}

	m.releases[project][version] = url
}

func (m *gitHubMock) Releases(project string, all bool) ([]*Release, error) {
	if proj, ok := m.releases[project]; ok {
		var releases []*Release
		for v, url := range proj {
			releases = append(releases, &Release{
				Tag:  v,
				Docs: url,
			})
		}
		sort.Sort(byTag(releases))
		return releases, nil
	}

	return nil, nil
}

func (m *gitHubMock) Release(project, version string) (*Release, error) {
	if proj, ok := m.releases[project]; ok {
		if rel, ok := proj[version]; ok {
			return &Release{
				Tag:  version,
				Docs: rel,
			}, nil
		}
	}

	return nil, fmt.Errorf("not found")
}

func newTestSrv(github GitHub) *DocSrv {
	return &DocSrv{
		"",
		github,
		new(sync.RWMutex),
		make(map[string]latestVersion),
		new(sync.RWMutex),
		make(map[string]struct{}),
		new(sync.RWMutex),
		make(map[string][]*version),
	}
}

func tarGzServer() (string, func()) {
	server := httptest.NewServer(http.HandlerFunc(tarGzMakefileHandler))
	return server.URL, server.Close
}

const testMakefile = `
build:
	@OUTPUT=$(DESTINATION_FOLDER)/out; \
	echo "$(BASE_URL)" >> $$OUTPUT; \
	echo "$(SHARED_REPO_FOLDER)" >> $$OUTPUT;
`

func tarGzMakefileHandler(w http.ResponseWriter, r *http.Request) {
	gw := gzip.NewWriter(w)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	err := tw.WriteHeader(&tar.Header{
		Name:    "Makefile",
		Mode:    0777,
		Size:    int64(len([]byte(testMakefile))),
		ModTime: time.Now(),
	})
	if err != nil {
		return
	}

	io.Copy(tw, bytes.NewBuffer([]byte(testMakefile)))
}