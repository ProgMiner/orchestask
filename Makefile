
CMDS = orchestask orchadmin orchman

.PHONY: all clean
.SUFFIXES:

all: clean $(CMDS)

clean:
	@rm -vf $(CMDS)

$(CMDS):
	go build ./.../$@
