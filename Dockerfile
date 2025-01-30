# Default image run by meshsim, based on Debian stable.
# It can be used as an example for custom images.
FROM docker.io/debian:stable

# Install some useful network and debug tools.
RUN apt-get update && apt-get install -y \
    procps \
    strace \
    net-tools \
    iproute2 \
    tcpdump \
    traceroute \
    mtr-tiny \
    iputils-ping \
    less \
    lsof \
    netcat-openbsd

# Include the topologiser and run it on startup.
COPY --from=gitlab.lip6.fr:5050/ie6/meshsim/topologiser:latest /bin/topologiser /bin/topologiser
CMD ["/bin/topologiser"]
