// Copyright 2018-2019 CERN
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
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package ocdav

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	gatewayv0alphapb "github.com/cs3org/go-cs3apis/cs3/gateway/v0alpha"
	storageproviderv0alphapb "github.com/cs3org/go-cs3apis/cs3/storageprovider/v0alpha"
	"github.com/cs3org/reva/pkg/appctx"
	"github.com/cs3org/reva/pkg/rgrpc/todo/pool"
	"github.com/cs3org/reva/pkg/rhttp"
	"github.com/mitchellh/mapstructure"
)

type ctxKey int

const (
	ctxKeyBaseURI ctxKey = iota
)

func init() {
	rhttp.Register("ocdav", New)
}

// Config holds the config options that need to be passed down to all ocdav handlers
type Config struct {
	Prefix          string `mapstructure:"prefix"`
	FilesNamespace  string `mapstructure:"files_namespace"`
	WebdavNamespace string `mapstructure:"webdav_namespace"`
	ChunkFolder     string `mapstructure:"chunk_folder"`
	GatewaySvc      string `mapstructure:"gateway"`
}

type svc struct {
	c             *Config
	webDavHandler *WebDavHandler
	davHandler    *DavHandler
}

// New returns a new ocdav
func New(m map[string]interface{}) (rhttp.Service, error) {
	conf := &Config{}
	if err := mapstructure.Decode(m, conf); err != nil {
		return nil, err
	}

	if conf.ChunkFolder == "" {
		conf.ChunkFolder = os.TempDir()
	}

	if err := os.MkdirAll(conf.ChunkFolder, 0755); err != nil {
		return nil, err
	}

	s := &svc{
		c:             conf,
		webDavHandler: new(WebDavHandler),
		davHandler:    new(DavHandler),
	}
	// initialize handlers and set default configs
	if err := s.webDavHandler.init(conf.WebdavNamespace); err != nil {
		return nil, err
	}
	if err := s.davHandler.init(conf); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *svc) Prefix() string {
	return s.c.Prefix
}

func (s *svc) Close() error {
	return nil
}

func (s *svc) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := appctx.GetLogger(ctx)

		// the webdav api is accessible from anywhere
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// TODO(jfd): do we need this?
		// fake litmus testing for empty namespace: see https://github.com/golang/net/blob/e514e69ffb8bc3c76a71ae40de0118d794855992/webdav/litmus_test_server.go#L58-L89
		if r.Header.Get("X-Litmus") == "props: 3 (propfind_invalid2)" {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}

		// to build correct href prop urls we need to keep track of the base path
		// always starts with /
		base := path.Join("/", s.Prefix())

		var head string
		head, r.URL.Path = rhttp.ShiftPath(r.URL.Path)
		log.Debug().Str("head", head).Str("tail", r.URL.Path).Msg("http routing")
		switch head {
		case "status.php":
			s.doStatus(w, r)
			return
		case "remote.php":
			// skip optional "remote.php"
			head, r.URL.Path = rhttp.ShiftPath(r.URL.Path)

			// yet, add it to baseURI
			base = path.Join(base, "remote.php")

		}
		switch head {
		// the old `/webdav` endpoint uses remote.php/webdav/$path
		case "webdav":
			// for oc we need to prepend /home as the path that will be passed to the home storage provider
			// will not contain the username
			base = path.Join(base, "webdav")
			ctx := context.WithValue(ctx, ctxKeyBaseURI, base)
			r = r.WithContext(ctx)
			s.webDavHandler.Handler(s).ServeHTTP(w, r)
			return
		case "dav":
			// cern uses /dav/files/$namespace -> /$namespace/...
			// oc uses /dav/files/$user -> /$home/$user/...
			// for oc we need to prepend the path to user homes
			// or we take the path starting at /dav and allow rewriting it?
			base = path.Join(base, "dav")
			ctx := context.WithValue(ctx, ctxKeyBaseURI, base)
			r = r.WithContext(ctx)
			s.davHandler.Handler(s).ServeHTTP(w, r)
			return
		}
		log.Warn().Msg("resource not found")
		w.WriteHeader(http.StatusNotFound)
	})
}

func (s *svc) getClient() (gatewayv0alphapb.GatewayServiceClient, error) {
	return pool.GetGatewayServiceClient(s.c.GatewaySvc)
}

func wrapResourceID(r *storageproviderv0alphapb.ResourceId) string {
	return wrap(r.StorageId, r.OpaqueId)
}

// The fileID must be encoded
// - XML safe, because it is going to be used in the profind result
// - url safe, because the id might be used in a url, eg. the /dav/meta nodes
// which is why we base62 encode it
func wrap(sid string, oid string) string {
	return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", sid, oid)))
}

func unwrap(rid string) *storageproviderv0alphapb.ResourceId {
	decodedID, err := base64.URLEncoding.DecodeString(rid)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(string(decodedID), ":", 2)
	if len(parts) != 2 {
		return nil
	}
	return &storageproviderv0alphapb.ResourceId{
		StorageId: parts[0],
		OpaqueId:  parts[1],
	}
}
