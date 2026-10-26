package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

func init() {
	register("whoami", "show the account, tenant and role the API resolves, and where the credential came from", runWhoami)
}

// whoami reads GET /status (operationId status): the service echoes the
// federated account, tenant and role v3 resolved for this credential.
func (c *client) status(ctx context.Context) (map[string]any, error) {
	var r struct {
		Data map[string]any `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/status", nil, &r); err != nil {
		return nil, err
	}
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	return r.Data, nil
}

// pick returns the first non-empty string at any of the keys, looking at the
// top level and one level down (account/tenant objects), because the payload
// shape is the service's to evolve.
func pick(d map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := d[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			for _, n := range []string{"name", "slug", "email", "username", "id"} {
				if s, ok := v[n].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return "-"
}

// signedOutFix is the fix for the source the credential came from.
func signedOutFix(source string) string {
	switch {
	case len(source) >= 6 && source[:6] == "plugin":
		return "run /enforcer:login in Claude Code"
	case source == "GRAPH_AUTH_HELPER":
		return "run /enforcer:login, or check what GRAPH_AUTH_HELPER prints"
	case source == "--api-key-file":
		return "check the key in the --api-key-file"
	case source == "GRAPH_API_KEY" || source == "ENFORCER_API_KEY":
		return "check the " + source + " variable"
	}
	return "check enforcer.api_key in ~/.enforcer/credentials.json, or run /enforcer:login"
}

func runWhoami(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(env.errOut, "usage: graphwatch whoami")
		return exitUsage
	}
	d, err := env.client.status(ctx)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusUnauthorized {
			fmt.Fprintf(env.errOut, "graphwatch: signed out or key rejected (credential source: %s): %s\n",
				env.source, signedOutFix(env.source))
			return exitRuntime
		}
		return reportError(env.errOut, err)
	}
	if env.json {
		d["credential_source"] = env.source
		b, _ := json.MarshalIndent(d, "", "  ")
		fmt.Fprintln(env.out, string(b))
		return exitOK
	}
	env.table([]string{"FIELD", "VALUE"}, [][]string{
		{"base url", env.base},
		{"credential", env.source},
		{"account", pick(d, "account", "email", "username", "account_id")},
		{"tenant", pick(d, "tenant", "tenant_name", "tenant_id")},
		{"role", pick(d, "role")},
	})
	return exitOK
}
