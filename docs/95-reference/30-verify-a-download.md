# Verify a download

Every release artifact rta publishes carries provenance you can check before you run it: the archives, the container image and the Helm chart. None of this is needed to use rta, and all of it is worth a minute before you put a binary where an agent can launch it.

## The release archive

The checksum proves the download is intact; the attestation proves the archive was built by this repository's release workflow from the tagged commit — provenance, not just integrity:

```bash
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
shasum -a 256 -c checksums.txt --ignore-missing     # sha256sum on Linux
gh attestation verify rta_*_"${os}"_"${arch}".tar.gz --owner this-is-tobi
```

The release also carries a keyless cosign signature over `checksums.txt`, whose sha256 lines are what tie every archive to it. It is bound to the workflow that made it, and that workflow is the reusable attestation step in [this-is-tobi/github-workflows](https://github.com/this-is-tobi/github-workflows) at its `v0` tag — the certificate names the workflow that ran the signing step, not the repository that called it — which is why the identity below names that repository rather than this one:

```bash
cosign verify-blob checksums.txt --bundle checksums.txt.cosign.bundle \
  --certificate-identity-regexp '^https://github.com/this-is-tobi/github-workflows/\.github/workflows/attest-go\.yml@refs/tags/v0$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## The container image

```bash
gh attestation verify oci://ghcr.io/this-is-tobi/rta:latest --owner this-is-tobi
```

The cosign signature is the one an admission controller can enforce. It is bound to the reusable attestation workflow that signed the digest, at its `v0` tag, for the reason the binaries section gives:

```bash
cosign verify ghcr.io/this-is-tobi/rta:latest \
  --certificate-identity-regexp '^https://github.com/this-is-tobi/github-workflows/\.github/workflows/attest-docker\.yml@refs/tags/v0$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## The Helm chart

It moves on its own version stream, and the rta it deploys is its `appVersion`. Verify it the way you verify the image:

```bash
gh attestation verify oci://ghcr.io/this-is-tobi/rta/rta-chart:<version> --owner this-is-tobi
```

And its cosign signature, bound to the chart attestation workflow the same way:

```bash
cosign verify ghcr.io/this-is-tobi/rta/rta-chart:<version> \
  --certificate-identity-regexp '^https://github.com/this-is-tobi/github-workflows/\.github/workflows/attest-helm\.yml@refs/tags/v0$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Related

- [Installation](../10-getting-started/10-installation.md) — putting the verified binary on your `$PATH`
- [Other ways to install](../10-getting-started/40-other-ways-to-install.md) — the image and the chart
