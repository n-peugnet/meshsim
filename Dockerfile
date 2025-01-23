FROM golang AS build
WORKDIR /src

# Download Go modules
COPY topologiser/go.mod topologiser/go.sum ./
RUN go mod download

# Build the binary
COPY topologiser .
RUN CGO_ENABLED=0 go build -o /bin/topologiser

# Copy only the binary in final image
FROM scratch
COPY --from=build /bin/topologiser /bin/topologiser
CMD ["/bin/topologiser"]
