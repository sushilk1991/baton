package runner

// Linux aligns siginfo_t's child union to eight bytes on amd64/arm64.
const waitAlignBytes = 4
