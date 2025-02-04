all: .BUILDDATE

.BUILDDATE: topologiser supervisord.conf Dockerfile Makefile
	docker build -t gitlab.lip6.fr:5050/ie6/meshsim:latest .
	date > $@

topologiser: .FORCE
	$(MAKE) -C $@ $(MAKECMDGOALS)

clean: topologiser
	rm -rf __pycache__
	rm -f .BUILDDATE

.PHONY: all clean
.FORCE:
