all: .BUILDDATE

.BUILDDATE: $(wildcard *.go) go.mod go.sum Dockerfile Makefile
	docker build -t gitlab.lip6.fr:5050/ie6/meshsim/topologiser:latest .
	date > $@
	@touch .

clean:
	rm -f .BUILDDATE

.PHONY: all clean
