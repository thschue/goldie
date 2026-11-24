package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/engine"
)

// badBundleTarget is the 400 envelope for a missing or unknown bundle target.
// It names the valid one and echoes nothing of the request.
var badBundleTarget = envelope("bad_request", "invalid target",
	"Pass target as claude.")

// maxBundleBytes is the most a bundle archive may weigh: what the hardened
// client reads, so the server never sends an archive its own client would
// refuse.
const maxBundleBytes = contract.MaxResponseBytes

// bundleTooLarge is the 422 envelope for an archive over maxBundleBytes. The
// client words the same limit for an archive a server sent anyway.
var bundleTooLarge = envelope("bundle_too_large", "the bundle is larger than 16 MiB",
	"Shrink or remove a skill with large files (binary assets) so the bundle fits in 16 MiB.")

// GetBundle answers GET /api/v1/bundle: the resolved set of the Project's view
// as a gzip-compressed tar laid out for the target, with the set digest as its
// ETag. The same set always gives the same ETag; gzip bytes can differ between Go
// toolchains, so clients compare ETags, not bytes. An If-None-Match that names
// the ETag is a 304 with no body, before the archive is built. An archive over
// maxBundleBytes is refused whole (422), never cut.
func (s *Server) GetBundle(_ context.Context, req contract.GetBundleRequestObject) (contract.GetBundleResponseObject, error) {
	if !engine.ValidBundleTarget(req.Params.Target) {
		return contract.GetBundle400JSONResponse(badBundleTarget), nil
	}
	project, ok := validProject(req.Params.Project)
	if !ok {
		return contract.GetBundle400JSONResponse(badProject), nil
	}
	snap := s.provider()
	if snap == nil {
		return contract.GetBundle503JSONResponse{NotReadyJSONResponse: contract.NotReadyJSONResponse(notReady)}, nil
	}
	proj, _ := scopeHint(snap, project)
	files, digest := snap.Bundle(proj, req.Params.Target)
	etag := `"` + digest + `"`
	if req.Params.IfNoneMatch != nil && etagMatches(*req.Params.IfNoneMatch, etag) {
		return contract.GetBundle304Response{Headers: contract.GetBundle304ResponseHeaders{ETag: etag}}, nil
	}
	archive, err := tarGz(files)
	if err != nil {
		return nil, err
	}
	if len(archive) > maxBundleBytes {
		return contract.GetBundle422JSONResponse(bundleTooLarge), nil
	}
	return contract.GetBundle200ApplicationgzipResponse{
		Body:          bytes.NewReader(archive),
		ContentLength: int64(len(archive)),
		Headers:       contract.GetBundle200ResponseHeaders{ETag: etag},
	}, nil
}

// etagMatches reports whether an If-None-Match value, a list of entity tags or
// *, names etag. A weak tag matches its strong form, as for a GET.
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}

// tarGz encodes files as a gzip-compressed tar of regular files. Nothing in
// the headers varies with time or host; the ETag, not the bytes, is the identity.
func tarGz(files []engine.BundleFile) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		mode := int64(0o644)
		if f.Exec {
			mode = 0o755
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: f.Path, Mode: mode, Size: int64(len(f.Data)), Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
