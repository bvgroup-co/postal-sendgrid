# syntax=docker/dockerfile:1
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/postal-sendgrid .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/postal-sendgrid /app/postal-sendgrid
VOLUME ["/data"]
ENV LISTEN_ADDR=:8080
ENV DATABASE_PATH=/data/postal-sendgrid.db
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/postal-sendgrid"]
