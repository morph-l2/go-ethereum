package poseidon2

import (
	"fmt"
	"testing"
)

func TestPoseidonCodeHash(t *testing.T) {
	// nil
	got := fmt.Sprintf("%s", CodeHash(nil))
	want := "0x2ed1da00b14d635bd35b88ab49390d5c13c90da7e9e3a5f1ea69cd87a0aa3e82"

	if got != want {
		t.Errorf("got %q, wanted %q", got, want)
	}

	// single byte
	got = fmt.Sprintf("%s", CodeHash([]byte{0}))
	want = "0x2989a45a95109328f0d2461d4afb3e94c3217821954e7856d9a27af392e04bc6"

	if got != want {
		t.Errorf("got %q, wanted %q", got, want)
	}

	got = fmt.Sprintf("%s", CodeHash([]byte{1}))
	want = "0x143649b3a06b23ed7e9ba3e1a9096e3fccd6dd83a226f4299d9585f1baccc667"

	if got != want {
		t.Errorf("got %q, wanted %q", got, want)
	}

	// 32 bytes
	bytes := make([]byte, 32)
	for i := range bytes {
		bytes[i] = 1
	}
	got = fmt.Sprintf("%s", CodeHash(bytes))
	want = "0x052c860a8d4e109a897163ac2b32592906694bc664104ec118c776aa07cfd91f"
	if got != want {
		t.Errorf("got %q, wanted %q", got, want)
	}
}
