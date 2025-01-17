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

package auth

import (
	"context"
	"fmt"
	"strings"

	userproviderv0alphapb "github.com/cs3org/go-cs3apis/cs3/userprovider/v0alpha"
	"github.com/cs3org/reva/pkg/appctx"
	"github.com/cs3org/reva/pkg/rgrpc"
	"github.com/cs3org/reva/pkg/token"
	tokenmgr "github.com/cs3org/reva/pkg/token/manager/registry"
	"github.com/cs3org/reva/pkg/user"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultHeader   = "x-access-token"
	defaultPriority = 100
)

func init() {
	rgrpc.RegisterUnaryInterceptor("auth", NewUnary)
	rgrpc.RegisterStreamInterceptor("auth", NewStream)
}

type config struct {
	// TODO(labkode): access a map is more performant as uri as fixed in length
	// for SkipMethods.
	Priority      int                               `mapstructure:"priority"`
	SkipMethods   []string                          `mapstructure:"skip_methods"`
	Header        string                            `mapstructure:"header"`
	TokenManager  string                            `mapstructure:"token_manager"`
	TokenManagers map[string]map[string]interface{} `mapstructure:"token_managers"`
}

func parseConfig(m map[string]interface{}) (*config, error) {
	c := &config{}
	if err := mapstructure.Decode(m, c); err != nil {
		err = errors.Wrap(err, "auth: error decoding conf")
		return nil, err
	}
	return c, nil
}

func skip(url string, skipped []string) bool {
	for _, s := range skipped {
		if strings.HasPrefix(s, url) {
			return true
		}
	}
	return false
}

// NewUnary returns a new unary interceptor that adds
// trace information for the request.
func NewUnary(m map[string]interface{}) (grpc.UnaryServerInterceptor, int, error) {
	conf, err := parseConfig(m)
	if err != nil {
		err = errors.Wrap(err, "auth: error parsing config")
		return nil, 0, err
	}

	if conf.Header == "" {
		conf.Header = defaultHeader
	}

	if conf.Priority == 0 {
		conf.Priority = defaultPriority
	}

	if conf.TokenManager == "" {
		err := errors.New("auth: token manager is not configured for interceptor")
		return nil, 0, err
	}
	h, ok := tokenmgr.NewFuncs[conf.TokenManager]
	if !ok {
		return nil, 0, errors.New("auth: token manager does not exist: " + conf.TokenManager)
	}

	tokenManager, err := h(conf.TokenManagers[conf.TokenManager])
	if err != nil {
		return nil, 0, errors.Wrap(err, "auth: error creating token manager")
	}

	interceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, span := trace.StartSpan(ctx, "auth")
		defer span.End()
		log := appctx.GetLogger(ctx)

		if skip(info.FullMethod, conf.SkipMethods) {
			span.AddAttributes(trace.BoolAttribute("auth_enabled", false))
			log.Debug().Str("method", info.FullMethod).Msg("skipping auth")
			return handler(ctx, req)
		}

		span.AddAttributes(trace.BoolAttribute("auth_enabled", true))

		tkn, _ := token.ContextGetToken(ctx)

		if tkn == "" {
			log.Warn().Msg("access token not found")
			return nil, status.Errorf(codes.Unauthenticated, "auth: core access token not found")
		}

		// validate the token
		u, err := tokenManager.DismantleToken(ctx, tkn)
		if err != nil {
			log.Warn().Msg("access token is invalid")
			return nil, status.Errorf(codes.Unauthenticated, "auth: core access token is invalid")
		}

		// store user and core access token in context.
		span.AddAttributes(
			trace.StringAttribute("id.idp", u.Id.Idp),
			trace.StringAttribute("id.opaque_id", u.Id.OpaqueId),
			trace.StringAttribute("username", u.Username),
			trace.StringAttribute("token", tkn))
		span.AddAttributes(trace.StringAttribute("user", u.String()), trace.StringAttribute("token", tkn))

		ctx = user.ContextSetUser(ctx, u)
		ctx = token.ContextSetToken(ctx, tkn)
		return handler(ctx, req)
	}
	return interceptor, conf.Priority, nil
}

// NewStream returns a new server stream interceptor
// that adds trace information to the request.
func NewStream(m map[string]interface{}) (grpc.StreamServerInterceptor, int, error) {
	conf, err := parseConfig(m)
	if err != nil {
		return nil, 0, err
	}

	if conf.Header == "" {
		conf.Header = defaultHeader
	}

	if conf.Priority == 0 {
		conf.Priority = defaultPriority
	}

	h, ok := tokenmgr.NewFuncs[conf.TokenManager]
	if !ok {
		return nil, 0, fmt.Errorf("auth: token manager not found: %s", conf.TokenManager)
	}

	tokenManager, err := h(conf.TokenManagers[conf.TokenManager])
	if err != nil {
		return nil, 0, errors.New("auth: token manager not found: " + conf.TokenManager)
	}

	interceptor := func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		log := appctx.GetLogger(ctx)

		if skip(info.FullMethod, conf.SkipMethods) {
			log.Debug().Str("method", info.FullMethod).Msg("skiping auth")
			return handler(srv, ss)
		}

		tkn, _ := token.ContextGetToken(ctx)

		if tkn == "" {
			log.Warn().Msg("access token not found")
			return status.Errorf(codes.Unauthenticated, "auth: core access token not found")
		}

		// validate the token
		claims, err := tokenManager.DismantleToken(ctx, tkn)
		if err != nil {
			log.Warn().Msg("access token invalid")
			return status.Errorf(codes.Unauthenticated, "auth: core access token is invalid")
		}

		u := &userproviderv0alphapb.User{}
		if err := mapstructure.Decode(claims, u); err != nil {
			log.Warn().Msg("user claims invalid")
			return status.Errorf(codes.Unauthenticated, "auth: claims are invalid")
		}

		// store user and core access token in context.
		ctx = user.ContextSetUser(ctx, u)
		ctx = token.ContextSetToken(ctx, tkn)

		wrapped := newWrappedServerStream(ctx, ss)
		return handler(srv, wrapped)
	}
	return interceptor, conf.Priority, nil
}

func newWrappedServerStream(ctx context.Context, ss grpc.ServerStream) *wrappedServerStream {
	return &wrappedServerStream{ServerStream: ss, newCtx: ctx}
}

type wrappedServerStream struct {
	grpc.ServerStream
	newCtx context.Context
}

func (ss *wrappedServerStream) Context() context.Context {
	return ss.newCtx
}
