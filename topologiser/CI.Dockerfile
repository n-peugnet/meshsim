# Dockerfile to use with buildx to cross-compile multi-arch images
FROM --platform=$BUILDPLATFORM golang AS build
WORKDIR /src

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /bin/topologiser

# Copy only the binary in final image
FROM scratch
COPY --from=build /bin/topologiser /bin/topologiser
CMD ["/bin/topologiser"]
