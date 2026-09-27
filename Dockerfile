FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go processes.go plex.go transcoder.go notifications.go notification_alerts.go activity.go service_alerts.go arr.go arr_replacement.go infinidysk.go ./
COPY static ./static
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/silo-monitor .
RUN mkdir -p /out/data

FROM alpine:3.22 AS certificates
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/silo-monitor /silo-monitor
COPY --from=build --chown=65532:65532 /out/data /data

USER 65532:65532
ENV ACTIVITY_DATA_DIR=/data/activity
ENV REPLACEMENT_DATA_DIR=/data/replacements
EXPOSE 8080
ENTRYPOINT ["/silo-monitor"]