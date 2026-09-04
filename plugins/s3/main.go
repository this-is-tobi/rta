// Command rta-plugin-s3 talks to an S3-compatible object store: buckets,
// objects, presigned URLs and bucket policy.
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

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "s3",
		Summary: "S3-compatible object storage: buckets, objects, presigned URLs and policy",
		Version: version,
		Capabilities: []plugin.Capability{
			overviewCapability(),
			bucketListCapability(),
			policyGetCapability(),
			s3ObjectListCapability(),
			s3ObjectTreeCapability(),
			s3ObjectShowCapability(),
			s3ObjectGetCapability(),
			s3ObjectSetCapability(),
			s3ObjectCopyCapability(),
			s3ObjectRenameCapability(),
			s3ObjectRemoveCapability(),
			s3ObjectPresignCapability(),
			s3BucketDownloadCapability(),
			s3BucketUploadCapability(),
		},
	}
}
