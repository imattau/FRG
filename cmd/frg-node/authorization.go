package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type rpcRole string

const (
	roleAdmin     rpcRole = "admin"
	roleSubmitter rpcRole = "submitter"
	roleObserver  rpcRole = "observer"
	roleValidator rpcRole = "validator"
)

type rpcAuthorizer struct {
	roles           map[string]rpcRole
	requireIdentity bool
}

func newRPCAuthorizer(configured map[string]string, requireIdentity bool) (*rpcAuthorizer, error) {
	roles := make(map[string]rpcRole, len(configured))
	for fingerprint, configuredRole := range configured {
		if _, err := hex.DecodeString(fingerprint); err != nil || len(fingerprint) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid client certificate fingerprint %q", fingerprint)
		}
		role := rpcRole(configuredRole)
		switch role {
		case roleAdmin, roleSubmitter, roleObserver, roleValidator:
			roles[fingerprint] = role
		default:
			return nil, fmt.Errorf("invalid RPC role %q", configuredRole)
		}
	}
	return &rpcAuthorizer{roles: roles, requireIdentity: requireIdentity}, nil
}

func (a *rpcAuthorizer) authorize(ctx context.Context, required rpcRole) error {
	if a == nil {
		return nil
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		if a.requireIdentity {
			return status.Error(codes.PermissionDenied, "RPC client identity is not authenticated")
		}
		return nil // Loopback/non-TLS mode is protected by the listener boundary.
	}
	var state *credentials.TLSInfo
	switch info := p.AuthInfo.(type) {
	case credentials.TLSInfo:
		state = &info
	case *credentials.TLSInfo:
		state = info
	default:
		return status.Error(codes.PermissionDenied, "RPC client identity is not authenticated")
	}
	if state == nil || len(state.State.PeerCertificates) == 0 {
		if !a.requireIdentity {
			return nil
		}
		return status.Error(codes.PermissionDenied, "RPC client certificate is missing")
	}
	fingerprint := sha256.Sum256(state.State.PeerCertificates[0].Raw)
	role, ok := a.roles[hex.EncodeToString(fingerprint[:])]
	if !ok || !roleAllows(role, required) {
		return status.Error(codes.PermissionDenied, "RPC client is not authorized")
	}
	return nil
}

func roleAllows(actual, required rpcRole) bool {
	if actual == roleAdmin || actual == required {
		return true
	}
	return required != roleAdmin && actual == roleValidator
}
