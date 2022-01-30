all: phpkd

phpkd: phpkd.go libphpkd.a
	CGO_LDFLAGS="-L. -lphpkd \
	$(shell php-config --libs) \
	-L$(shell php-config --prefix)/lib -lphp" \
	go build -gcflags '-N -l' -v -x $<

libphpkd.a: phpkd.o
	$(AR) rcs $@ $<

phpkd.o: phpkd.c
	$(CC) -Wall -Wextra -Werror -std=c11 -pedantic \
	$(shell php-config --includes) \
	-g -O0 -c $< -o $@

clean:
	rm -f phpkd.o libphpkd.a phpkd

.PHONY: all clean
