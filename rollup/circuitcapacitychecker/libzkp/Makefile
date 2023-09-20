.PHONY: clean libzkp

clean:
	rm -f *.a *.so *.dylib
	cargo clean

libzkp:
	cargo build --release
	find $(PWD)/target/release | grep -E 'libzkp.so|libzkp.dylib' | xargs -I{} cp {} $(PWD)/
	find $(PWD)/target | grep libzktrie.so | xargs -I{} cp {} $(PWD)/
