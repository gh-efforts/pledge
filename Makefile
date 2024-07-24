pledge: deps
	rm -f pledge
	go build ./

deps:
	git submodule update --init --recursive
	make -C extern/filecoin-ffi
