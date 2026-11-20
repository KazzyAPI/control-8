FROM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24@sha256:d2d2bc1c84f7e60d7d2438a3836ae7d0c847f4888464e7ec9ba3a1339a1ee804 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /control8 ./cmd/control8
RUN mkdir /empty

FROM gcr.io/distroless/static@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /control8 /control8
# empty dir owned by 10001 so named volumes inherit ownership (ARC-010)
COPY --from=build --chown=10001:10001 /empty /var/lib/control8
USER 10001:10001
EXPOSE 8000
ENTRYPOINT ["/control8"]
