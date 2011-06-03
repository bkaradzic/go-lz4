include $(GOROOT)/src/Make.inc

TARG=lz4
GOFILES= \
	lz4/reader.go \
	lz4/writer.go

include $(GOROOT)/src/Make.pkg

