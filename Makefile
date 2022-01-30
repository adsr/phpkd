all: phpkd

phpkd: phpkd.go libphpkd.a
	CGO_LDFLAGS='-L. -lphpkd' go build -v -x $<

libphpkd.a: phpkd.o
	$(AR) rcs $@ $<

phpkd.o: phpkd.c
	$(CC) -Wall -Wextra -Werror -std=c99 -pedantic -c $< -o $@

clean:
	rm -f phpkd.o libphpkd.a phpkd

.PHONY: all clean
