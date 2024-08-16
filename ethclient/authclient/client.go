package authclient

import (
	"context"

	"github.com/morph-l2/go-ethereum/node"
	"github.com/morph-l2/go-ethereum/rpc"
)

type Client struct {
	c *rpc.Client
}

// DialContext connects a client to the given URL.
func DialContext(ctx context.Context, rawurl string, jwtsecret [32]byte) (*Client, error) {
	auth := rpc.WithHTTPAuth(node.NewJWTAuth(jwtsecret))
	c, err := rpc.DialOptions(ctx, rawurl, auth)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}
