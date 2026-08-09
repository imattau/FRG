package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func authContext(certRaw []byte) context.Context {
	cert := &x509.Certificate{Raw: certRaw}
	info := credentials.TLSInfo{}
	info.State.PeerCertificates = []*x509.Certificate{cert}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: info})
}

func TestRPCAuthorizerEnforcesRoles(t *testing.T) {
	certRaw := []byte("submitter-cert")
	fingerprint := sha256.Sum256(certRaw)
	authorizer, err := newRPCAuthorizer(map[string]string{hex.EncodeToString(fingerprint[:]): "submitter"}, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := authContext(certRaw)
	if err := authorizer.authorize(ctx, roleSubmitter); err != nil {
		t.Fatalf("submitter was rejected: %v", err)
	}
	if status.Code(authorizer.authorize(ctx, roleObserver)) != codes.PermissionDenied {
		t.Fatal("submitter unexpectedly received observer access")
	}
}

func TestRPCAuthorizerRejectsUnknownRemoteClient(t *testing.T) {
	authorizer, err := newRPCAuthorizer(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if status.Code(authorizer.authorize(context.Background(), roleObserver)) != codes.PermissionDenied {
		t.Fatal("unauthenticated remote client was accepted")
	}
}
