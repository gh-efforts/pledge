pledge: deps
	rm pledge
	go build ./

deps:
	git submodule update --init --recursive
	make -C extern/filecoin-ffi
