package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// doctorServerChecks probes the selected server without starting it or reading a key.
func (a *app) doctorServerChecks(ctx context.Context) []doctorCheck {
	srv, err := a.resolveServer("")
	if err != nil && asError(err).Code == "server_not_selected" {
		known, _, knownErr := a.knownServers()
		if knownErr == nil && len(known) == 0 {
			srv, err = a.localServer(), nil
		}
	}
	if err != nil {
		return []doctorCheck{problem("server", levelWarning, "server_not_selected", err.Error(), "choose a server with aboard servers use NAME")}
	}
	local := srv.URL == a.localServer().URL
	name, label := "server", "server"
	if local {
		name, label = "local_server", "local server"
	}
	c, err := a.newClient(srv, "", time.Second)
	var info *api.ServerInfo
	if err == nil {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		info, err = c.info(probe)
	}
	if err != nil {
		if sandbox, ok := a.networkBlocked(); ok {
			return []doctorCheck{problem(name, levelError, "sandbox_blocks_network", label+" at "+srv.URL+" can't be reached from "+sandbox+"'s sandbox, which blocks network access", "run "+allowFix+" in a terminal, or approve this command outside the sandbox")}
		}
		fix := "check the server or the network"
		message := label + " at " + srv.URL + " can't be reached"
		if local {
			fix = "run aboard up, or any command that needs it starts it"
			message = "local server not running at " + srv.URL
		}
		return []doctorCheck{problem(name, levelWarning, "server_unreachable", message, fix)}
	}
	reach := okCheck(name, label+" running at "+srv.URL)
	if b := infoBuild(info); local && info.Mode == api.Local && compareBuilds(b, currentBuild()) < 0 {
		reach = problem("local_server", levelWarning, "server_outdated", "local server at "+srv.URL+" runs aboard "+buildLabel(b)+", older than this aboard "+buildLabel(currentBuild()), "run aboard status, or any other command that uses it, which replaces it")
	}
	return []doctorCheck{reach, versionCheck(srv, info)}
}

func versionCheck(srv serverRef, info *api.ServerInfo) doctorCheck {
	return checkVersionSkew(srv.URL, version, info.Version)
}

func checkVersionSkew(server, client, remote string) doctorCheck {
	message := fmt.Sprintf("server at %s runs aboard %s; this CLI runs aboard %s", server, remote, client)
	cv, sv := skewVersion(client), skewVersion(remote)
	if !cv.ok || !sv.ok {
		return problem("server_version", levelWarning, "version_unknown", message+"; compatibility can't be assessed", "check aboard version and the server's version with its admin")
	}
	step := 0
	if cv.core[0] == 0 && sv.core[0] == 0 {
		step = 1
	}
	// Subtract the smaller number so even the largest parsed version cannot overflow.
	gap := max(cv.core[step], sv.core[step]) - min(cv.core[step], sv.core[step])
	if gap <= 1 {
		return okCheck("server_version", message+"; within the supported version window")
	}
	fix := "ask the server's admin to upgrade aboard on " + server
	if compareVersions(client, remote) < 0 {
		fix = "run aboard upgrade in your own terminal to update this CLI"
	}
	return problem("server_version", levelWarning, "version_skew", message+"; outside the supported version window", fix)
}

// skewVersion accepts semantic version labels, including a development build's metadata.
func skewVersion(label string) semver {
	text := strings.TrimPrefix(label, "v")
	core, metadata, hasMetadata := strings.Cut(text, "+")
	core, pre, hasPre := strings.Cut(core, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}
	}
	for _, part := range parts {
		if !versionIdentifier(part, true) {
			return semver{}
		}
	}
	if hasPre {
		for _, part := range strings.Split(pre, ".") {
			if !versionIdentifier(part, false) {
				return semver{}
			}
			if numericIdentifier(part) && len(part) > 1 && part[0] == '0' {
				return semver{}
			}
		}
	}
	if hasMetadata {
		for _, part := range strings.Split(metadata, ".") {
			if !versionIdentifier(part, false) {
				return semver{}
			}
		}
	}
	return parseVersion(label)
}

func numericIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func versionIdentifier(s string, core bool) bool {
	if s == "" {
		return false
	}
	if core {
		return numericIdentifier(s) && (len(s) == 1 || s[0] != '0')
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '-' {
			return false
		}
	}
	return true
}
