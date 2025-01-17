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

package rhttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cs3org/reva/internal/http/interceptors/appctx"
	"github.com/cs3org/reva/internal/http/interceptors/log"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
)

// NewMiddlewares contains all the registered new middleware functions.
var NewMiddlewares = map[string]NewMiddleware{}

// NewMiddleware is the function that HTTP middlewares need to register at init time.
type NewMiddleware func(conf map[string]interface{}) (Middleware, int, error)

// RegisterMiddleware registers a new HTTP middleware and its new function.
func RegisterMiddleware(name string, n NewMiddleware) {
	NewMiddlewares[name] = n
}

// middlewareTriple represents a middleware with the
// priority to be chained.
type middlewareTriple struct {
	Name       string
	Priority   int
	Middleware Middleware
}

// Middleware is a middleware http handler.
type Middleware func(h http.Handler) http.Handler

// Services is a map of service name and its new function.
var Services = map[string]NewService{}

// Register registers a new HTTP services with name and new function.
func Register(name string, newFunc NewService) {
	Services[name] = newFunc
}

// NewService is the function that HTTP services need to register at init time.
type NewService func(conf map[string]interface{}) (Service, error)

type config struct {
	Network            string                            `mapstructure:"network"`
	Address            string                            `mapstructure:"address"`
	Services           map[string]map[string]interface{} `mapstructure:"services"`
	EnabledServices    []string                          `mapstructure:"enabled_services"`
	Middlewares        map[string]map[string]interface{} `mapstructure:"middlewares"`
	EnabledMiddlewares []string                          `mapstructure:"enabled_middlewares"`
}

// Server contains the server info.
type Server struct {
	httpServer  *http.Server
	conf        *config
	listener    net.Listener
	svcs        map[string]Service
	handlers    map[string]http.Handler
	middlewares []*middlewareTriple
	log         zerolog.Logger
}

// New returns a new server
func New(m interface{}, l zerolog.Logger) (*Server, error) {
	conf := &config{}
	if err := mapstructure.Decode(m, conf); err != nil {
		return nil, err
	}

	// apply defaults
	if conf.Network == "" {
		conf.Network = "tcp"
	}

	if conf.Address == "" {
		conf.Address = "localhost:9998"
	}

	httpServer := &http.Server{}
	s := &Server{
		httpServer: httpServer,
		conf:       conf,
		svcs:       map[string]Service{},
		handlers:   map[string]http.Handler{},
		log:        l,
	}
	return s, nil
}

// Start starts the server
func (s *Server) Start(ln net.Listener) error {
	if err := s.registerServices(); err != nil {
		return err
	}

	if err := s.registerMiddlewares(); err != nil {
		return err
	}

	s.httpServer.Handler = s.getHandler()
	s.listener = ln

	s.log.Info().Msgf("http server listening at %s://%s", "http", s.conf.Address)
	err := s.httpServer.Serve(s.listener)
	if err == nil || err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Stop stops the server.
func (s *Server) Stop() error {
	s.closeServices()
	// TODO(labkode): set ctx deadline to zero
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// TODO(labkode): we can't stop the server shutdown because a service cannot be shutdown.
// What do we do in case a service cannot be properly closed? Now we just log the error.
// TODO(labkode): the close should be given a deadline using context.Context.
func (s *Server) closeServices() {
	for _, svc := range s.svcs {
		if err := svc.Close(); err != nil {
			s.log.Error().Err(err).Msgf("error closing service %q", svc.Prefix())
		} else {
			s.log.Info().Msgf("service %q correctly closed", svc.Prefix())
		}
	}
}

// Network return the network type.
func (s *Server) Network() string {
	return s.conf.Network
}

// Address returns the network address.
func (s *Server) Address() string {
	return s.conf.Address
}

// GracefulStop gracefully stops the server.
func (s *Server) GracefulStop() error {
	s.closeServices()
	return s.httpServer.Shutdown(context.Background())
}

func (s *Server) isService(svcName string) bool {
	for key := range Services {
		if key == svcName {
			return true
		}
	}
	return false
}

func (s *Server) isMiddlewareEnabled(name string) bool {
	for _, key := range s.conf.EnabledMiddlewares {
		if key == name {
			return true
		}
	}
	return false
}

func (s *Server) registerMiddlewares() error {
	middlewares := []*middlewareTriple{}
	for name, newFunc := range NewMiddlewares {
		if s.isMiddlewareEnabled(name) {
			m, prio, err := newFunc(s.conf.Middlewares[name])
			if err != nil {
				err = errors.Wrapf(err, "error creating new middleware: %s,", name)
				return err
			}
			middlewares = append(middlewares, &middlewareTriple{
				Name:       name,
				Priority:   prio,
				Middleware: m,
			})
			s.log.Info().Msgf("http middleware enabled: %s", name)
		}
	}
	s.middlewares = middlewares
	return nil
}

func (s *Server) registerServices() error {
	for _, svcName := range s.conf.EnabledServices {
		if s.isService(svcName) {
			newFunc := Services[svcName]
			svc, err := newFunc(s.conf.Services[svcName])
			if err != nil {
				err = errors.Wrapf(err, "http service %s could not be started,", svcName)
				return err
			}

			// instrument services with opencensus tracing.
			h := traceHandler(svcName, svc.Handler())
			s.handlers[svc.Prefix()] = h
			s.svcs[svc.Prefix()] = svc
			s.log.Info().Msgf("http service enabled: %s@/%s", svcName, svc.Prefix())
		} else {
			message := fmt.Sprintf("http service %s does not exist", svcName)
			return errors.New(message)
		}
	}
	return nil
}

func traceHandler(name string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := trace.StartSpan(r.Context(), name)
		r = r.WithContext(ctx)
		h.ServeHTTP(w, r)
		span.End()
	})
}

func (s *Server) getHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head, tail := ShiftPath(r.URL.Path)
		if h, ok := s.handlers[head]; ok {
			r.URL.Path = tail
			s.log.Debug().Msgf("http routing: head=%s tail=%s svc=%s", head, r.URL.Path, head)
			h.ServeHTTP(w, r)
			return
		}

		// when a service is exposed at the root.
		if h, ok := s.handlers[""]; ok {
			r.URL.Path = "/" + head + tail
			s.log.Debug().Msgf("http routing: head= tail=%s svc=root", r.URL.Path)
			h.ServeHTTP(w, r)
			return
		}

		s.log.Debug().Msgf("http routing: head=%s tail=%s svc=not-found", head, tail)
		w.WriteHeader(http.StatusNotFound)
	})

	// sort middlewares by priority.
	sort.SliceStable(s.middlewares, func(i, j int) bool {
		return s.middlewares[i].Priority > s.middlewares[j].Priority
	})

	handler := http.Handler(h)

	for _, triple := range s.middlewares {
		s.log.Info().Msgf("chainning http middleware %s with priority  %d", triple.Name, triple.Priority)
		handler = triple.Middleware(traceHandler(triple.Name, handler))
	}

	// add always the logctx middleware as most priority, this middleware is internal
	// and cannot be configured from the configuration.
	coreMiddlewares := []*middlewareTriple{}
	coreMiddlewares = append(coreMiddlewares, &middlewareTriple{Middleware: log.New(), Name: "log"})
	coreMiddlewares = append(coreMiddlewares, &middlewareTriple{Middleware: appctx.New(s.log), Name: "appctx"})

	for _, triple := range coreMiddlewares {
		handler = triple.Middleware(traceHandler(triple.Name, handler))
	}

	// use opencensus handler to trace endpoints.
	// TODO(labkode): enable also opencensus telemetry.
	handler = &ochttp.Handler{
		Handler: handler,
		//IsPublicEndpoint: true,
	}

	return handler
}

// ShiftPath splits off the first component of p, which will be cleaned of
// relative components before processing. head will never contain a slash and
// tail will always be a rooted path without trailing slash.
// see https://blog.merovius.de/2017/06/18/how-not-to-use-an-http-router.html
// and https://gist.github.com/weatherglass/62bd8a704d4dfdc608fe5c5cb5a6980c#gistcomment-2161690 for the zero alloc code below
func ShiftPath(p string) (head, tail string) {
	if p == "" {
		return "", "/"
	}
	p = strings.TrimPrefix(path.Clean(p), "/")
	i := strings.Index(p, "/")
	if i < 0 {
		return p, "/"
	}
	return p[:i], p[i:]
}

// Service represents a HTTP service.
type Service interface {
	Handler() http.Handler
	Prefix() string
	Close() error
}
