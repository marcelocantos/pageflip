module github.com/marcelocantos/pageflip/meetcat

go 1.26.1

require (
	github.com/google/uuid v1.6.0
	github.com/marcelocantos/claudia v0.6.0
)

require golang.org/x/sys v0.43.0 // indirect

replace github.com/marcelocantos/claudia => ../../claudia
