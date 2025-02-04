# Default image run by meshsim, based on Debian stable.
# It can be used as an example for custom images.
FROM docker.io/debian:stable

# Install some useful network and debug tools.
RUN apt-get update && apt-get install -y \
    supervisor \
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

# Include prometheus node exporter
COPY --from=docker.io/prom/node-exporter /bin/node_exporter /bin/node_exporter

# Include the topologiser
COPY --from=gitlab.lip6.fr:5050/ie6/meshsim/topologiser:latest /bin/topologiser /bin/topologiser

# Include supervisord configuration and run it on startup
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf
CMD ["/usr/bin/supervisord"]
