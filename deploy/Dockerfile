# aihub — single static Go binary (SQLite embedded). git is needed at runtime
# (the per-WI bare repos and the git smart-HTTP proxy shell out to it); curl
# serves the compose healthcheck.
# All state lives under /data (mount ONE volume): aihub.db + git/ + memory/.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY aihub /aihub
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/aihub", "-data-dir", "/data", "-listen", ":8080"]
