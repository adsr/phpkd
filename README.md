# phpkd - PHP Prefork Daemon

phpkd is a PHP SAPI implementation that runs under Go's `net/http` server. It
operates similar to `mod_php` under Apache's prefork mpm.

### Build

Requirements:

* Go (only tested with 1.17.6)
* PHP compiled with `--enable-embed --enable-zts` (only tested with 8.2)
* A C compiler like `gcc` or `clang`

Run `make` to build.

### Synopsis

Run server:
```
user@host:~/phpkd$ ./phpkd -h
Usage of ./phpkd:
  -addr string
        listen address (default ":8080")
  -handler string
        PHP handler path (default "index.php")
  -threads int
        number of worker threads (default 8)
user@host:~/phpkd$ cat -n index.php
     1    <?php
     2
     3    header('An-arbitrary: header');
     4
     5    print_r([
     6        '_SERVER' => $_SERVER,
     7        '_GET' => $_GET,
     8        '_POST' => $_POST,
     9    ]);
user@host:~/phpkd$ ./phpkd -handler=index.php
[I] 2022/02/19 21:40:08.048720 server.run: ListenAndServe
...
127.0.0.1:35570 - - [19/Feb/2022:16:40:09 -0500] - "POST /req/uri?getvar=42 HTTP/1.1" 0 954
...
^C
[E] 2022/02/19 21:40:35.401885 handleSignals: Received signal: interrupt
[I] 2022/02/19 21:40:35.402031 server.run: ListenAndServe finish=http: Server closed
[I] 2022/02/19 21:40:35.402968 server.run: Workers finished
```

Test with `curl`:
```
user@host:~/phpkd$ curl -v -d postvar=hello 'localhost:8080/req/uri?getvar=42'
*   Trying 127.0.0.1:8080...
* Connected to localhost (127.0.0.1) port 8080 (#0)
> POST /req/uri?getvar=42 HTTP/1.1
> Host: localhost:8080
> User-Agent: curl/7.81.0
> Accept: */*
> Content-Length: 13
> Content-Type: application/x-www-form-urlencoded
>
* Mark bundle as not supporting multiuse
< HTTP/1.1 200 OK
< An-Arbitrary: header
< Content-Type: text/html; charset=UTF-8
< X-Powered-By: PHP/8.2.0-dev
< Date: Sat, 19 Feb 2022 21:40:09 GMT
< Content-Length: 954
<
Array
(
    [_SERVER] => Array
        (
            [HTTP_USER_AGENT] => curl/7.81.0
            [HTTP_ACCEPT] => */*
            [HTTP_CONTENT_LENGTH] => 13
            [CONTENT_TYPE] => application/x-www-form-urlencoded
            [SERVER_SOFTWARE] => phpkd/0.1.0
            [REMOTE_ADDR] => 127.0.0.1
            [REMOTE_PORT] => 35570
            [REQUEST_SCHEME] =>
            [SERVER_PROTOCOL] => HTTP/1.1
            [REQUEST_METHOD] => POST
            [QUERY_STRING] => getvar=42
            [REQUEST_URI] => /req/uri?getvar=42
            [SCRIPT_NAME] => index.php
            [REQUEST_TIME_FLOAT] => 1645306809.5246
            [REQUEST_TIME] => 1645306809
            [argv] => Array
                (
                    [0] => getvar=42
                )

            [argc] => 1
        )

    [_GET] => Array
        (
            [getvar] => 42
        )

    [_POST] => Array
        (
            [postvar] => hello
        )

)
* Connection #0 to host localhost left intact
```
