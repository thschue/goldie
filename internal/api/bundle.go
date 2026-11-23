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

// GetBundle answers GET /api/v1/bundle: the resolved set of the Project's view
// as a gzip-compressed tar laid out for the target, with the set digest as its
// ETag. The same set always gives the same bytes. An If-None-Match that names
// the ETag is a 304 with no body, before the archive is built.
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
// the headers varies with time or host, so the bytes are a function of files.
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
