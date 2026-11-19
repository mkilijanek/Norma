module github.com/Autumn-27/norma

go 1.26.11

require (
	github.com/creack/pty v1.1.24
	github.com/hinshun/vt10x v0.0.0-20220301184237-5011da428d02
	golang.org/x/net v0.55.0
)

require gopkg.in/yaml.v3 v3.0.1

// Dependencies are pinned to forks under github.com/mkilijanek (same commits as upstream).
replace (
	github.com/creack/pty => github.com/mkilijanek/pty v1.1.24
	github.com/hinshun/vt10x => github.com/mkilijanek/vt10x v0.0.0-20220301184237-5011da428d02
	golang.org/x/net => github.com/mkilijanek/net v0.55.0
	gopkg.in/yaml.v3 => github.com/mkilijanek/yaml v0.0.0-20220527083530-f6f7691b1fde
)
