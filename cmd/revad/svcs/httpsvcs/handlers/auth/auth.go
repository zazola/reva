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
	"net/http"

	authv0alphapb "github.com/cernbox/go-cs3apis/cs3/auth/v0alpha"
	rpcpb "github.com/cernbox/go-cs3apis/cs3/rpc"
	"github.com/cernbox/reva/cmd/revad/httpserver"
	"github.com/cernbox/reva/cmd/revad/svcs/httpsvcs/handlers/auth/credential/registry"
	tokenregistry "github.com/cernbox/reva/cmd/revad/svcs/httpsvcs/handlers/auth/token/registry"
	tokenwriterregistry "github.com/cernbox/reva/cmd/revad/svcs/httpsvcs/handlers/auth/tokenwriter/registry"
	"github.com/cernbox/reva/pkg/log"
	"github.com/pkg/errors"

	"github.com/cernbox/reva/pkg/token"
	tokenmgr "github.com/cernbox/reva/pkg/token/manager/registry"
	"github.com/cernbox/reva/pkg/user"
	"github.com/mitchellh/mapstructure"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var logger = log.New("auth")

func init() {
	httpserver.RegisterMiddleware("auth", New)
}

type config struct {
	Priority             int                               `mapstructure:"priority"`
	AuthSVC              string                            `mapstructure:"authsvc"`
	CredentialStrategy   string                            `mapstructure:"credential_strategy"`
	CredentialStrategies map[string]map[string]interface{} `mapstructure:"credential_strategies"`
	TokenStrategy        string                            `mapstructure:"token_strategy"`
	TokenStrategies      map[string]map[string]interface{} `mapstructure:"token_strategies"`
	TokenManager         string                            `mapstructure:"token_manager"`
	TokenManagers        map[string]map[string]interface{} `mapstructure:"token_managers"`
	TokenWriter          string                            `mapstructure:"token_writer"`
	TokenWriters         map[string]map[string]interface{} `mapstructure:"token_writers"`
}

func parseConfig(m map[string]interface{}) (*config, error) {
	c := &config{}
	if err := mapstructure.Decode(m, c); err != nil {
		logger.Error(context.Background(), errors.Wrap(err, "error decoding conf"))
		return nil, err
	}
	return c, nil
}

// New returns a new middleware with defined priority.
func New(m map[string]interface{}) (httpserver.Middleware, int, error) {
	conf, err := parseConfig(m)
	if err != nil {
		return nil, 0, err
	}

	f, ok := registry.NewCredentialFuncs[conf.CredentialStrategy]
	if !ok {
		return nil, 0, fmt.Errorf("credential strategy not found: %s", conf.CredentialStrategy)
	}

	credStrategy, err := f(conf.CredentialStrategies[conf.CredentialStrategy])
	if err != nil {
		return nil, 0, err
	}

	g, ok := tokenregistry.NewTokenFuncs[conf.TokenStrategy]
	if !ok {
		return nil, 0, fmt.Errorf("token strategy not found: %s", conf.TokenStrategy)
	}

	tokenStrategy, err := g(conf.TokenStrategies[conf.TokenStrategy])
	if err != nil {
		return nil, 0, err
	}

	h, ok := tokenmgr.NewFuncs[conf.TokenManager]
	if !ok {
		return nil, 0, fmt.Errorf("token manager not found: %s", conf.TokenStrategy)
	}

	tokenManager, err := h(conf.TokenManagers[conf.TokenManager])
	if err != nil {
		return nil, 0, err
	}

	i, ok := tokenwriterregistry.NewTokenFuncs[conf.TokenWriter]
	if !ok {
		return nil, 0, fmt.Errorf("token writer not found: %s", conf.TokenWriter)
	}

	tokenWriter, err := i(conf.TokenWriters[conf.TokenWriter])
	if err != nil {
		return nil, 0, err
	}

	chain := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// check for token
			tkn := tokenStrategy.GetToken(r)
			if tkn == "" {
				logger.Println(r.Context(), "core access token not set")
				creds, err := credStrategy.GetCredentials(w, r)
				if err != nil {
					logger.Error(r.Context(), err)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				req := &authv0alphapb.GenerateAccessTokenRequest{
					ClientId:     creds.ClientID,
					ClientSecret: creds.ClientSecret,
				}
				client, err := getAuthClient(conf.AuthSVC)
				if err != nil {
					logger.Error(r.Context(), err)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				res, err := client.GenerateAccessToken(r.Context(), req)
				if err != nil {
					logger.Error(r.Context(), err)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if res.Status.Code != rpcpb.Code_CODE_OK {
					logger.Error(r.Context(), fmt.Errorf("code=%d", res.Status.Code))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				logger.Println(r.Context(), "core access token generated")
				// write token to response
				tkn = res.AccessToken
				tokenWriter.WriteToken(tkn, w)
			} else {
				logger.Println(r.Context(), "core access token is set")
			}

			// validate token
			claims, err := tokenManager.DismantleToken(r.Context(), tkn)
			if err != nil {
				logger.Error(r.Context(), err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			u := &user.User{}
			if err := mapstructure.Decode(claims, u); err != nil {
				logger.Error(r.Context(), err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// store user and core access token in context.
			ctx := user.ContextSetUser(r.Context(), u)
			ctx = token.ContextSetToken(ctx, tkn)

			ctx = metadata.AppendToOutgoingContext(ctx, "x-access-token", tkn) // FIXME hardcoded metadata key. use  PerRPCCredentials?

			r = r.WithContext(ctx)
			h.ServeHTTP(w, r)
		})
	}
	return chain, conf.Priority, nil
}

// TODO(labkode): re-use connection using mutex.
func getAuthClient(host string) (authv0alphapb.AuthServiceClient, error) {
	conn, err := getConn(host)
	if err != nil {
		return nil, err
	}
	return authv0alphapb.NewAuthServiceClient(conn), nil
}

func getConn(host string) (*grpc.ClientConn, error) {
	return grpc.Dial(host, grpc.WithInsecure())
}
