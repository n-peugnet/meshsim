all: .BUILDDATE

.BUILDDATE: $(wildcard *.go) go.mod go.sum Dockerfile Makefile
	docker build -t gitlab.lip6.fr:5050/ie6/meshsim/topologiser:latest .
	date > $@
	@touch .

clean:
	rm -f .BUILDDATE c.out

dummynet:
	ip link add eth0 type dummy
	ip addr add 172.22.0.3/24 dev eth0
	ip link set eth0 up
	ip route add default via 172.22.0.1 dev eth0

_test: dummynet
	go test -v -coverprofile=c.out ./...

test:
	go test -c -o=/dev/null ./...
	unshare -rn $(MAKE) _test

.PHONY: all clean dummynet _test test
