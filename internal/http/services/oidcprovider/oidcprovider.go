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

package oidcprovider

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/storage"
	"github.com/ory/fosite/token/jwt"
	"github.com/pkg/errors"

	userproviderv0alphapb "github.com/cs3org/go-cs3apis/cs3/userprovider/v0alpha"
	"github.com/cs3org/reva/pkg/appctx"
	"github.com/cs3org/reva/pkg/rhttp"
	"github.com/mitchellh/mapstructure"
)

func init() {
	rhttp.Register("oidcprovider", New)
}

type config struct {
	Prefix          string                            `mapstructure:"prefix"`
	GatewayEndpoint string                            `mapstructure:"gateway"`
	Clients         map[string]map[string]interface{} `mapstructure:"clients"`
	Issuer          string                            `mapstructure:"issuer"`
}

type client struct {
	ID            string   `mapstructure:"id"`
	Secret        string   `mapstructure:"client_secret,"`
	RedirectURIs  []string `mapstructure:"redirect_uris"`
	GrantTypes    []string `mapstructure:"grant_types"`
	ResponseTypes []string `mapstructure:"response_types"`
	Scopes        []string `mapstructure:"scopes"`
	Audience      []string `mapstructure:"audience"`
	Public        bool     `mapstructure:"public"`
}

type svc struct {
	prefix  string
	conf    *config
	handler http.Handler
	store   *storage.MemoryStore
	oauth2  fosite.OAuth2Provider
	clients map[string]fosite.Client
}

// New returns a new oidcprovidersvc
func New(m map[string]interface{}) (rhttp.Service, error) {
	c := &config{}
	if err := mapstructure.Decode(m, c); err != nil {
		return nil, err
	}

	if c.Prefix == "" {
		c.Prefix = "oauth2"
	}

	// parse clients
	clients := map[string]fosite.Client{}
	for id, val := range c.Clients {
		client := &client{}
		if err := mapstructure.Decode(val, client); err != nil {
			err = errors.Wrap(err, "oidcprovider: error decoding client configuration")
			return nil, err
		}

		fosClient := &fosite.DefaultClient{
			ID:            client.ID,
			Secret:        []byte(client.Secret),
			RedirectURIs:  client.RedirectURIs,
			GrantTypes:    client.GrantTypes,
			ResponseTypes: client.ResponseTypes,
			Scopes:        client.Scopes,
			Audience:      client.Audience,
			Public:        client.Public,
		}

		clients[id] = fosClient
	}

	store := newExampleStore(clients)

	s := &svc{
		conf:    c,
		prefix:  c.Prefix,
		clients: clients,
		// This is an exemplary storage instance. We will add a client and a user to it so we can use these later on.
		store: store,
		oauth2: compose.Compose(
			fconfig,
			store,
			start,
			nil, // filled in by Compose based on the hash cost in the config

			// enabled handlers
			compose.OAuth2AuthorizeExplicitFactory,
			compose.OAuth2AuthorizeImplicitFactory,
			compose.OAuth2ClientCredentialsGrantFactory,
			compose.OAuth2RefreshTokenGrantFactory,
			compose.OAuth2ResourceOwnerPasswordCredentialsFactory,

			// be aware that open id connect factories need to be added after oauth2 factories to work properly.
			compose.OpenIDConnectExplicitFactory,
			compose.OpenIDConnectImplicitFactory,
			compose.OpenIDConnectHybridFactory,
			compose.OpenIDConnectRefreshFactory,

			compose.OAuth2TokenRevocationFactory,
			compose.OAuth2TokenIntrospectionFactory,

			// needs to come last
			compose.OAuth2PKCEFactory,
		),
	}
	s.setHandler()
	return s, nil
}

func (s *svc) Close() error {
	return nil
}

func (s *svc) Prefix() string {
	return s.prefix
}

func (s *svc) Handler() http.Handler {
	return s.handler
}

func (s *svc) setHandler() {
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := appctx.GetLogger(r.Context())

		if r.Method == "OPTIONS" {
			// TODO use CORS allow access from everywhere
			w.Header().Set("Access-Control-Allow-Origin", "*")
			return
		}

		var head string
		head, r.URL.Path = rhttp.ShiftPath(r.URL.Path)
		log.Info().Msgf("oidcprovider routing: head=%s tail=%s", head, r.URL.Path)
		switch head {
		case "":
			s.doHome(w, r)
		case "auth":
			s.doAuth(w, r)
		case "token":
			s.doToken(w, r)
		case "revoke":
			s.doRevoke(w, r)
		case "introspect":
			s.doIntrospect(w, r)
		case "userinfo":
			s.doUserinfo(w, r)
		case "sessions":
			// TODO(jfd) make session lookup configurable? only for development?
			s.doSessions(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newExampleStore(clients map[string]fosite.Client) *storage.MemoryStore {
	return &storage.MemoryStore{
		IDSessions:             make(map[string]fosite.Requester),
		Clients:                clients,
		AuthorizeCodes:         map[string]storage.StoreAuthorizeCode{},
		Implicit:               map[string]fosite.Requester{},
		AccessTokens:           map[string]fosite.Requester{},
		RefreshTokens:          map[string]fosite.Requester{},
		PKCES:                  map[string]fosite.Requester{},
		AccessTokenRequestIDs:  map[string]string{},
		RefreshTokenRequestIDs: map[string]string{},
	}
}

var fconfig = new(compose.Config)

// Because we are using oauth2 and open connect id, we use this little helper to combine the two in one
// variable.
var start = compose.CommonStrategy{
	// alternatively you could use:
	//  OAuth2Strategy: compose.NewOAuth2JWTStrategy(mustRSAKey())
	// TODO(jfd): generate / read proper secret from config
	CoreStrategy: compose.NewOAuth2HMACStrategy(fconfig, []byte("some-super-cool-secret-that-nobody-knows"), nil),

	// open id connect strategy
	OpenIDConnectTokenStrategy: compose.NewOpenIDConnectStrategy(fconfig, mustRSAKey()),
}

// customSession keeps track of the session between the auth and token and userinfo endpoints.
// We need our custom session to store the internal token.
type customSession struct {
	*openid.DefaultSession
	internalToken string
	fosite.Session
}

func (s *customSession) SetExpiresAt(key fosite.TokenType, exp time.Time) {
	s.DefaultSession.SetExpiresAt(key, exp)
}

func (s *customSession) GetExpiresAt(key fosite.TokenType) time.Time {
	return s.DefaultSession.GetExpiresAt(key)
}
func (s *customSession) GetUsername() string {
	return s.DefaultSession.GetUsername()
}

func (s *customSession) GetSubject() string {
	return s.DefaultSession.GetSubject()
}

func (s *customSession) Clone() fosite.Session {
	return s.DefaultSession.Clone()
}

// A session is passed from the `/auth` to the `/token` endpoint. You probably want to store data like: "Who made the request",
// "What organization does that person belong to" and so on.
// For our use case, the session will meet the requirements imposed by JWT access tokens, HMAC access tokens and OpenID Connect
// ID Tokens plus a custom field

// newSession is a helper function for creating a new session. This may look like a lot of code but since we are
// setting up multiple strategies it is a bit longer.
// Usually, you could do:
//
//  session = new(fosite.DefaultSession)
func (s *svc) newSession(token string, user *userproviderv0alphapb.User) *customSession {
	return &customSession{
		DefaultSession: &openid.DefaultSession{
			Claims: &jwt.IDTokenClaims{
				// TODO(labkode): we override the issuer here as we are the OIDC provider.
				// Does it make sense? The auth backend can be on another domain, but this service
				// is the one responsible for oidc logic.
				// The issuer needs to map the in the configuration.
				Issuer:  s.conf.Issuer,
				Subject: user.Id.OpaqueId,
				// TODO(labkode): check what audience means and set it correctly.
				//Audience:    []string{"https://my-client.my-application.com"},
				// TODO(labkode): make times configurable to align to internal token lifetime.
				ExpiresAt: time.Now().Add(time.Hour * 6),
				//IssuedAt:    time.Now(),
				//RequestedAt: time.Now(),
				//AuthTime:    time.Now(),
			},
			Headers: &jwt.Headers{
				Extra: make(map[string]interface{}),
			},
			Username: user.Username,
			Subject:  user.Id.OpaqueId,
		},
		internalToken: token,
	}
}

// emptySession creates a session object and fills it with safe defaults
func (s *svc) emptySession() *customSession {
	return &customSession{
		DefaultSession: &openid.DefaultSession{
			Claims: &jwt.IDTokenClaims{
				// TODO(labkode): we override the issuer here as we are the OIDC provider.
				// Does it make sense? The auth backend can be on another domain, but this service
				// is the one responsible for oidc logic.
				// The issuer needs to map the in the configuration.
				Issuer: s.conf.Issuer,
				// TODO(labkode): check what audience means and set it correctly.
				//Audience:    []string{"https://my-client.my-application.com"},
				// TODO(labkode): make times configurable to align to internal token lifetime.
				ExpiresAt: time.Now().Add(time.Hour * 6),
				//IssuedAt:    time.Now(),
				//RequestedAt: time.Now(),
				//AuthTime:    time.Now(),
			},
			Headers: &jwt.Headers{
				Extra: make(map[string]interface{}),
			},
		},
	}
}

func mustRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		// TODO(jfd): don't panic!
		panic(err)
	}
	return key
}
