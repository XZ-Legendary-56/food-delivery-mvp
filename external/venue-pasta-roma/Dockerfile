# syntax=docker/dockerfile:1

# Built with its OWN go.mod/go.sum, not the platform's — this Dockerfile
# only ever COPYs files from within external/venue-pasta-roma (its own
# build context, set by docker-compose.yml's `context:`), so there is no
# path by which it could pull in the platform's internal/ packages even by
# accident (docs/PLAN.md 8.1).
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/venue-pasta-roma ./cmd/venue-pasta-roma

FROM alpine:3.20
RUN apk add --no-cache ca-certificates \
    && addgroup -S app \
    && adduser -S app -G app
USER app

COPY --from=build /out/venue-pasta-roma /usr/local/bin/venue-pasta-roma
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/venue-pasta-roma"]
