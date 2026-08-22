// Command rta-plugin-s3 talks to an S3-compatible object store: buckets,
// objects, presigned URLs and bucket policy (PROJECT.md §7, Wave 2).
// Works against real AWS S3, MinIO, R2, Ceph — anything minio-go speaks —
// which is why every field is generic (endpoint, not "AWS region + account").
//
// Build it and put it on your $PATH as `rta-plugin-s3`:
//
//	cd plugins/s3 && go build -o ~/.local/bin/rta-plugin-s3 .
//
// State the endpoint once, in rta's config, under the artifact's own
// section — `rta explain s3.overview` prints the exact heading including
// the digest:
//
//	plugins:
//	  s3@<digest>:
//	    endpoint: s3.amazonaws.com
//	    region: us-west-2
//	    tls: true
//	    access-key: AKIA...
//
// and export RTA_S3_SECRET_KEY. Every capability here reaches off the box,
// so none of them — including s3.overview — appear on the automatic
// dashboard on their own (see cap's comment); add one explicitly once you
// have decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: s3.overview
package main

import (
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
)

func main() { sdk.Serve(Plugin()) }

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "s3",
		Summary: "S3-compatible object storage: buckets, objects, presigned URLs and policy",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			overviewCapability(),
			bucketListCapability(),
			policyGetCapability(),
			s3ObjectListCapability(),
			s3ObjectShowCapability(),
			s3ObjectGetCapability(),
			s3ObjectSetCapability(),
			s3ObjectCopyCapability(),
			s3ObjectRenameCapability(),
			s3ObjectRemoveCapability(),
			s3ObjectPresignCapability(),
		},
	}
}
