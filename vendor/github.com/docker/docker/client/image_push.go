package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
)

// ImagePush requests the docker host to push an image to a remote registry.
// It executes the privileged function if the operation is unauthorized
// and it tries one more time.
// It's up to the caller to handle the io.ReadCloser and close it properly.
func (cli *Client) ImagePush(ctx context.Context, image string, options image.PushOptions) (io.ReadCloser, error) {
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, err
	}

	if _, isCanonical := ref.(reference.Canonical); isCanonical {
		return nil, errors.New("cannot push a digest reference")
	}

	query := url.Values{}
	if !options.All {
		ref = reference.TagNameOnly(ref)
		if tagged, ok := ref.(reference.Tagged); ok {
			query.Set("tag", tagged.Tag())
		}
	}

	if options.Platform != nil {
		if err := cli.NewVersionError(ctx, "1.46", "platform"); err != nil {
			return nil, err
		}

		p := *options.Platform
		pJson, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("invalid platform: %v", err)
		}

		query.Set("platform", string(pJson))
	}

	resp, err := cli.tryImagePush(ctx, ref.Name(), query, options.RegistryAuth)
	if cerrdefs.IsUnauthorized(err) && options.PrivilegeFunc != nil {
		newAuthHeader, privilegeErr := options.PrivilegeFunc(ctx)
		if privilegeErr != nil {
			return nil, privilegeErr
		}
		resp, err = cli.tryImagePush(ctx, ref.Name(), query, newAuthHeader)
	}
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (cli *Client) tryImagePush(ctx context.Context, imageID string, query url.Values, registryAuth string) (*http.Response, error) {
	// Always send a body (which may be an empty JSON document ("{}")) to prevent
	// EOF errors on older daemons which had faulty fallback code for handling
	// authentication in the body when no auth-header was set, resulting in;
	//
	//	Error response from daemon: bad parameters and missing X-Registry-Auth: invalid X-Registry-Auth header: EOF
	//
	// We use [http.NoBody], which gets marshaled to an empty JSON document.
	//
	// see: https://github.com/moby/moby/commit/ea29dffaa541289591aa44fa85d2a596ce860e16
	return cli.post(ctx, "/images/"+imageID+"/push", query, http.NoBody, http.Header{
		registry.AuthHeader: {registryAuth},
	})
}
