# aihub — the server image: one static Go binary, pf (SQLite embedded),
# started as `pf aihub`. git is needed at runtime (the per-WI bare repos and
# the git smart-HTTP proxy shell out to it); curl serves the compose
# healthcheck.
# All state lives under /data (mount ONE volume): aihub.db + git/ + memory/.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY pf /pf
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/pf", "aihub", "-data-dir", "/data", "-listen", ":8080"]
