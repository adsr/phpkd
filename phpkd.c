#include <stdio.h>
#include "phpkd.h"

extern void gofunc(int x);

void cfunc(int x) {
    printf("in cfunc with %d\n", x);
    gofunc(x * 2);
}
