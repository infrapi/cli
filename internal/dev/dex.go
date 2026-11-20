package dev

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// The stack's identity provider is Dex, behind the gateway at
// <gateway>/dex: the browser logs in there, and Kayra reaches the same URL
// through APISIX as its proxy (remote.proxy), so the issuer is one for
// both. Its users are dev.yaml's, their password their users.password, or
// their id.

// DexClient is Kayra's client at Dex, a public one (PKCE).
const DexClient = "kayra-dev"

// dexConfig is the file Dex runs with: .infrapi-dev/dex/config.yaml.
const dexConfig = "dex/config.yaml"

// DexIssuer is Dex's issuer: through the gateway, as the browser sees it.
func (c *Config) DexIssuer() string {
	return fmt.Sprintf("http://localhost:%d/dex", c.Ports.Gateway)
}

// password is u's password at Dex: its own, or its id.
func (u User) password() string {
	if u.Password != "" {
		return u.Password
	}
	return u.ID
}

// email is u's e-mail, Dex's login: attributes.email, or <id>@example.com.
func (u User) email() string {
	if e := u.Attributes["email"]; e != "" {
		return e
	}
	return u.ID + "@example.com"
}

type dexPassword struct {
	Email             string `yaml:"email"`
	Hash              string `yaml:"hash"`
	Username          string `yaml:"username"`
	PreferredUsername string `yaml:"preferredUsername"`
	UserID            string `yaml:"userID"`
}

// renderDex is dex/config.yaml. A password's hash is the one already in
// dir's file when it still matches: bcrypt salts every hash, a new one
// each render would change the file every time.
func renderDex(c *Config, dir string) ([]byte, error) {
	old := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(dir, dexConfig)); err == nil { //#nosec G304 -- a file of the render's own dir
		var f struct {
			StaticPasswords []dexPassword `yaml:"staticPasswords"`
		}
		if yaml.Unmarshal(data, &f) == nil {
			for _, p := range f.StaticPasswords {
				old[p.UserID] = p.Hash
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	passwords := []dexPassword{}
	for _, u := range c.Users {
		hash := old[u.ID]
		if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(u.password())) != nil {
			h, err := bcrypt.GenerateFromPassword([]byte(u.password()), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			hash = string(h)
		}
		name := u.Attributes["name"]
		if name == "" {
			name = u.ID
		}
		passwords = append(passwords, dexPassword{Email: u.email(), Hash: hash, Username: name, PreferredUsername: u.ID, UserID: u.ID})
	}
	conf := map[string]any{
		"issuer":           c.DexIssuer(),
		"storage":          map[string]any{"type": "memory"},
		"web":              map[string]any{"http": "0.0.0.0:5556"},
		"oauth2":           map[string]any{"skipApprovalScreen": true},
		"enablePasswordDB": true,
		"staticClients": []map[string]any{{
			"id": DexClient, "name": "Kayra", "public": true,
			"redirectURIs": []string{fmt.Sprintf("http://localhost:%d/oauth2/callback", c.Ports.Kayra)},
		}},
		"staticPasswords": passwords,
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(conf); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return append([]byte("# Dex, the stack's identity provider, rendered by infrapi-cli dev render\n"+
		"# from dev.yaml's users and idp: edit dev.yaml. A user logs in with its\n"+
		"# e-mail and its users.password, its id when unset.\n"), out.Bytes()...), nil
}
