# Step 1: install dependencies and compile TypeScript
FROM node:22-alpine AS builder

WORKDIR /build

COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund

COPY tsconfig.json ./
COPY src ./src
RUN npm run build && npm prune --omit=dev

# Step 2: minimal runtime, non-root
FROM node:22-alpine

ENV NODE_ENV=production

WORKDIR /srv

COPY --from=builder /build/node_modules ./node_modules
COPY --from=builder /build/dist ./dist
COPY package.json ./

USER node

EXPOSE 8090

CMD ["node", "dist/index.js"]
