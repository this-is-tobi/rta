#!/bin/sh
# Entrypoint for the full image. Its only job is the credential decision that
# `rta plugin allow` records, and by default it does nothing at all.
#
# # Why this exists
#
# rta makes two decisions about a plugin, and the image can only honestly
# answer one of them. Trust — "these bytes may run" — is baked at build time,
# because the bytes were built in that build and the digest it pins is theirs.
# Allow — "these bytes may read my cluster credentials" — is a question about
# *your* machine and your kubeconfig, so the image cannot answer it for
# everyone who pulls it.
#
# But it is recorded in the state directory, and a container that is thrown
# away after every run has no state directory to record it in. Without this,
# using `kube` from an ephemeral container means running `rta plugin allow
# kube` inside every single one of them. With a persistent volume mounted at
# /rta-home the answer sticks and none of this is needed; this is for the
# `docker run --rm` case, where it otherwise cannot stick at all.
#
# # Why it is off unless you ask
#
# Opt-in, never a default, and the asymmetry is deliberate: an image that
# granted credential access on start would answer the more dangerous of rta's
# two questions on behalf of somebody who never read this file. Setting the
# variable is the act of answering it, and it is visible in whatever launched
# the container — a compose file, a `docker run` line, a pod spec — rather
# than buried in a layer.
#
#   RTA_ALLOW_PLUGINS=all          every bundled plugin
#   RTA_ALLOW_PLUGINS=kube,cnpg    only these
#   unset                          nothing, and `rta plugin list` says warn
#
# Only ever what a plugin already declares it needs: `rta plugin allow <name>`
# cannot grant a location the artifact never asked for, so the widest this
# reaches is still the union of what the bundled plugins declare.
#
# On Linux — which is what this image is — rta does not confine plugins, so
# the denial this lifts is not enforced by the OS the way it is on macOS. It
# still governs what rta itself will hand a plugin, and it is still the
# operator-facing record of the decision, which is why answering it
# deliberately is worth a variable rather than a shrug.
set -eu

allow="${RTA_ALLOW_PLUGINS:-}"
if [ -n "$allow" ]; then
	every=no
	case "$allow" in
	all)
		every=yes
		# Derived from what is actually in the image rather than a list
		# repeated here, so a plugin added to the build joins this by existing.
		names=$(find /usr/local/bin -name 'rta-plugin-*' -type f 2>/dev/null |
			sed 's|.*/rta-plugin-||' | sort)
		;;
	*)
		names=$(printf '%s' "$allow" | tr ',' ' ')
		;;
	esac

	for name in $names; do
		if out=$(rta plugin allow "$name" 2>&1); then
			continue
		fi
		# Most of the bundled plugins declare no credential location at all —
		# s3 and vault take their credentials as inputs, eol talks to a public
		# API — and `rta plugin allow` refuses those by design, because an
		# operator may only allow what an artifact actually asked for.
		#
		# Under `all` that is the ordinary case rather than a fault: "allow
		# whatever asks" has nothing to say about a plugin that asks for
		# nothing. Under an explicit list it is a fault, because somebody typed
		# that name expecting it to mean something.
		case "$out" in
		*plugin.allow.none*)
			if [ "$every" = yes ]; then
				continue
			fi
			;;
		esac
		# Loud rather than best-effort. This ran because somebody asked for it,
		# so a failure to honour it is a misconfiguration worth seeing at start
		# — the alternative is a capability refusing for an unrelated-looking
		# reason much later, with nothing connecting it back to here.
		echo "rta: RTA_ALLOW_PLUGINS names \"$name\", which could not be allowed:" >&2
		echo "$out" >&2
		exit 1
	done
fi

exec /usr/local/bin/rta "$@"
