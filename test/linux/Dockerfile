# The Linux test harness for kipple (design L12 of openspec/changes/linux-port).
# It runs the whole default suite as the unprivileged user kipple (uid 1000),
# whose home is a throwaway folder inside the container: the host's home is
# never mounted, and kipple refuses to run as root anyway. See test/linux/run.sh
# and the test-linux target in the Makefile.
#
# golang:1.23-bookworm, pinned by digest; it already has git and gcc for -race.
FROM golang:1.23-bookworm@sha256:60deed95d3888cc5e4d9ff8a10c54e5edc008c6ae3fba6187be6fb592e19e8c0

# jq for scripts/agent-flow-check.sh, attr (setfattr) for the xattr preflight.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends jq attr \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --uid 1000 --user-group --create-home --home-dir /home/kipple --shell /bin/bash kipple \
	&& mkdir -p /cache /xdev \
	&& chown kipple:kipple /cache

USER kipple
WORKDIR /home/kipple
# Go 1.23 records local telemetry counters under the home folder by default;
# turn that off once, at build time, so the home stays unchanged during a run.
RUN go telemetry off
