FROM golang:1.22.3-alpine3.20 AS build-stage
WORKDIR /app
COPY . .
RUN go build

FROM alpine:3.20.0
COPY --from=build-stage /app/k3d-registry-dockerd /usr/bin/k3d-registry-dockerd
EXPOSE 5000
CMD ["k3d-registry-dockerd"]