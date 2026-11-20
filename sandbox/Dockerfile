# Agent sandbox: Node 24 (the demo repos' toolchain) + git + the ap-runner.
# Debian, not alpine: the Agent SDK bundles a glibc-linked binary.
FROM node:24-bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/ap-runner
COPY runner/package.json runner/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY runner/tsconfig.json ./
COPY runner/src ./src
RUN npx tsc && npm prune --omit=dev --no-audit --no-fund

# Runs as the unprivileged image user (uid 1000). Task directories are bind
# mounted at /work; credentials arrive as ANTHROPIC_API_KEY or a copied login.
USER node
# No NODE_ENV=production: agents run `npm ci` in target repos and need devDependencies.
ENTRYPOINT ["node", "/opt/ap-runner/dist/index.js"]
