// Copyright 2016 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/googlecodelabs/tools/claat/parser"
	"github.com/googlecodelabs/tools/claat/types"
)

const (
	// supported codelab source types must be registered parsers
	// TODO: define these in claat/parser/..., e.g. in parser/gdoc
	srcInvalid   srcType = ""
	srcGoogleDoc srcType = "gdoc" // Google Docs doc
	srcMarkdown  srcType = "md"   // Markdown text

	// driveAPIBase is a base URL for Drive API v2
	driveAPIBase = "https://www.googleapis.com/drive/v2"
	// TODO: this v2, replace with
	// https://www.googleapis.com/drive/v3/files/id/export?mimeType=text/html
	driveAPIExport = "https://docs.google.com/feeds/download/documents/export/Export"

	driveMimeDocument = "application/vnd.google-apps.document"
	driveMimeFolder   = "application/vnd.google-apps.folder"
	driveExportMime   = "text/html"
)

// srcType is codelab source type
type srcType string

// resource is a codelab resource, loaded from local file
// or fetched from remote location.
type resource struct {
	typ  srcType       // source type
	body io.ReadCloser // resource body
	mod  time.Time     // last update of content
}

// codelab wraps types.Codelab, while adding source type
// and modified timestamp fields.
type codelab struct {
	*types.Codelab
	typ srcType   //  source type
	mod time.Time // last modified timestamp
}

// slurpCodelab retrieves and parses codelab source.
// It returns parsed codelab and its source type.
//
// The function will also fetch and parse fragments included
// with types.ImportNode.
func slurpCodelab(src string) (*codelab, error) {
	res, err := fetch(src)
	if err != nil {
		return nil, err
	}
	defer res.body.Close()
	clab, err := parser.Parse(string(res.typ), res.body)
	if err != nil {
		return nil, err
	}

	// fetch imports and parse them as fragments
	var imports []*types.ImportNode
	for _, st := range clab.Steps {
		imports = append(imports, importNodes(st.Content.Nodes)...)
	}
	ch := make(chan error, len(imports))
	defer close(ch)
	for _, imp := range imports {
		go func(n *types.ImportNode) {
			frag, err := slurpFragment(n.URL)
			if err != nil {
				ch <- fmt.Errorf("%s: %v", n.URL, err)
				return
			}
			n.Content.Nodes = frag
			ch <- nil
		}(imp)
	}
	for _ = range imports {
		if err := <-ch; err != nil {
			return nil, err
		}
	}

	v := &codelab{
		Codelab: clab,
		typ:     res.typ,
		mod:     res.mod,
	}
	return v, nil
}

func slurpFragment(url string) ([]types.Node, error) {
	res, err := fetchRemote(url, true)
	if err != nil {
		return nil, err
	}
	defer res.body.Close()
	return parser.ParseFragment(string(res.typ), res.body)
}

// fetch retrieves codelab doc either from local disk
// or a remote location.
// The caller is responsible for closing returned stream.
func fetch(name string) (*resource, error) {
	fi, err := os.Stat(name)
	if os.IsNotExist(err) {
		return fetchRemote(name, false)
	}
	r, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	return &resource{
		body: r,
		typ:  srcMarkdown,
		mod:  fi.ModTime(),
	}, nil
}

// fetchRemote retrieves resource r from the network.
//
// If urlStr is not a URL, i.e. does not have the host part, it is considered to be
// a Google Doc ID and fetched accordingly. Otherwise, a simple GET request
// is used to retrieve the contents.
//
// The caller is responsible for closing returned stream.
// If nometa is true, resource.mod may have zero value.
func fetchRemote(urlStr string, nometa bool) (*resource, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}
	if u.Host == "" || u.Host == "docs.google.com" {
		return fetchDriveFile(urlStr, nometa)
	}
	return fetchRemoteFile(urlStr)
}

// fetchRemoteFile retrieves codelab resource from url.
// It is a special case of fetchRemote function.
func fetchRemoteFile(url string) (*resource, error) {
	res, err := retryGet(nil, url, 3)
	if err != nil {
		return nil, err
	}
	t, err := http.ParseTime(res.Header.Get("last-modified"))
	if err != nil {
		t = time.Now()
	}
	return &resource{
		body: res.Body,
		mod:  t,
		typ:  srcMarkdown,
	}, nil
}

// fetchDriveFile uses Drive API to retrieve HTML representation of a Google Doc.
// See https://developers.google.com/drive/web/manage-downloads#downloading_google_documents
// for more details.
//
// If nometa is true, resource.mod will have zero value.
func fetchDriveFile(id string, nometa bool) (*resource, error) {
	id = gdocID(id)
	client, err := driveClient()
	if err != nil {
		return nil, err
	}

	if nometa {
		q := url.Values{"id": {id}, "exportFormat": {"html"}}
		u := fmt.Sprintf("%s?%s", driveAPIExport, q.Encode())
		res, err := retryGet(client, u, 7)
		if err != nil {
			return nil, err
		}
		return &resource{body: res.Body, typ: srcGoogleDoc}, nil
	}

	u := fmt.Sprintf("%s/files/%s?fields=id,mimeType,exportLinks,modifiedDate", driveAPIBase, id)
	res, err := retryGet(client, u, 7)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	meta := &struct {
		ID          string            `json:"id"`
		MimeType    string            `json:"mimeType"`
		ExportLinks map[string]string `json:"exportLinks"`
		Modified    time.Time         `json:"modifiedDate"`
	}{}
	if err := json.NewDecoder(res.Body).Decode(meta); err != nil {
		return nil, err
	}
	if meta.MimeType != driveMimeDocument {
		return nil, fmt.Errorf("%s: invalid mime type: %s", id, meta.MimeType)
	}
	link := meta.ExportLinks[driveExportMime]
	if link == "" {
		return nil, fmt.Errorf("%s: no %q export link", id, driveExportMime)
	}

	if res, err = retryGet(client, link, 7); err != nil {
		return nil, err
	}
	return &resource{
		body: res.Body,
		mod:  meta.Modified,
		typ:  srcGoogleDoc,
	}, nil
}

// downloadImages fetches imap images and stores them in dir/img directory, concurrently.
// The imap argument is expected to be a mapping of local file name to original image URL.
func downloadImages(client *http.Client, dir string, imap map[string]string) error {
	if len(imap) == 0 {
		return nil
	}
	// make sure img dir exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	ch := make(chan error, len(imap))
	for name, url := range imap {
		go func(name, url string) {
			ch <- slurpBytes(client, filepath.Join(dir, name), url, 5)
		}(name, url)
	}
	for _ = range imap {
		if err := <-ch; err != nil {
			return err
		}
	}
	return nil
}

// slurpBytes fetches a resource from url using retryGet and writes it to dst.
// It retries the fetch at most n times.
func slurpBytes(client *http.Client, dst, url string, n int) error {
	res, err := retryGet(client, url, n)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dst, b, 0644)
}

// retryGet tries to GET specified url up to n times.
// Default client will be used if not provided.
func retryGet(client *http.Client, url string, n int) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	for i := 0; i <= n; i++ {
		if i > 0 {
			t := time.Duration((math.Pow(2, float64(i)) + rand.Float64()) * float64(time.Second))
			time.Sleep(t)
		}
		res, err := client.Get(url)
		// return early with a good response
		// the rest is error handling
		if err == nil && res.StatusCode == http.StatusOK {
			return res, nil
		}

		// sometimes Drive API wouldn't even start a response,
		// we get net/http: TLS handshake timeout instead:
		// consider this a temporary failure and retry again
		if err != nil {
			continue
		}
		// otherwise, decode error response and check for "rate limit"
		defer res.Body.Close()
		var erres struct {
			Error struct {
				Errors []struct{ Reason string }
			}
		}
		b, _ := ioutil.ReadAll(res.Body)
		json.Unmarshal(b, &erres)
		var rateLimit bool
		for _, e := range erres.Error.Errors {
			if e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" {
				rateLimit = true
				break
			}
		}
		// this is neither a rate limit error, nor a server error:
		// retrying is useless
		if !rateLimit && res.StatusCode < http.StatusInternalServerError {
			return nil, fmt.Errorf("fetch %s: %s; %s", url, res.Status, b)
		}
	}
	return nil, fmt.Errorf("%s: failed after %d retries", url, n)
}

func gdocID(url string) string {
	const s = "/document/d/"
	if i := strings.Index(url, s); i >= 0 {
		url = url[i+len(s):]
	}
	if i := strings.IndexRune(url, '/'); i > 0 {
		url = url[:i]
	}
	return url
}
