FROM golang:1.22.3-alpine3.20 AS build-stage
WORKDIR /app
COPY . .
RUN go build

FROM alpine:3.20.0

# Build arguments provided by Docker buildx
ARG TARGETARCH
ARG ECR_CREDENTIAL_HELPER_VERSION=0.10.1

# Install curl and necessary dependencies for ECR credential helper
RUN apk add --no-cache curl

# Download and install ECR credential helper using Docker buildx TARGETARCH
RUN echo "Downloading ECR credential helper version ${ECR_CREDENTIAL_HELPER_VERSION} for architecture: ${TARGETARCH}" && \
    curl -L "https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com/${ECR_CREDENTIAL_HELPER_VERSION}/linux-${TARGETARCH}/docker-credential-ecr-login" \
        -o /usr/local/bin/docker-credential-ecr-login && \
    chmod +x /usr/local/bin/docker-credential-ecr-login

# Verify the installation
RUN docker-credential-ecr-login version

COPY --from=build-stage /app/k3d-registry-dockerd /usr/bin/k3d-registry-dockerd
EXPOSE 5000
CMD ["k3d-registry-dockerd"]