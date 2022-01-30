#include <SAPI.h>
#include <php_main.h>
#include <zend_stream.h>
#include <zend_signal.h>

#include "phpkd.h"

extern int gofunc(int x);

int phpkd_startup(sapi_module_struct *phpkd_module);
size_t phpkd_ub_write(const char *str, size_t len);
void phpkd_error(int type, const char *error_msg, ...);
void phpkd_log_message(const char *message, int syslog_type);
void phpkd_send_header(sapi_header_struct *sapi_header, void *server_context);

sapi_module_struct phpkd_module = {
	"phpkd",                       /* name */
	"PHP Prefork Daemon",          /* pretty name */

	phpkd_startup,                 /* startup */
	php_module_shutdown_wrapper,   /* shutdown */

	NULL,                          /* activate */
	NULL,                          /* deactivate */

	phpkd_ub_write,                /* unbuffered write */
	NULL,                          /* flush */
	NULL,                          /* get uid */
	NULL,                          /* getenv */

	phpkd_error,                   /* error handler */

	NULL,                          /* header handler */
	NULL,                          /* send headers handler */
	phpkd_send_header,             /* send header handler */

	NULL,                          /* read POST data */
	NULL,                          /* read Cookies */

	NULL,                          /* register server variables */
	phpkd_log_message,             /* Log message */
	NULL,                          /* Get request time */
	NULL,                          /* Child terminate */

	STANDARD_SAPI_MODULE_PROPERTIES
};

int phpkd_init() {
    gofunc(42);
    zend_signal_startup();
    sapi_startup(&phpkd_module);
    phpkd_module.startup(&phpkd_module);
    SG(options) |= SAPI_OPTION_NO_CHDIR;
    return SUCCESS;
}

int phpkd_request() {
    zend_file_handle fh;
    php_request_startup();
    zend_stream_init_filename(&fh, "test.php");
    php_execute_script(&fh);
    zend_destroy_file_handle(&fh);
    php_request_shutdown(NULL);
    return SUCCESS;
}

int phpkd_startup(sapi_module_struct *sapi_module) {
    return php_module_startup(sapi_module, NULL, 0);
}

size_t phpkd_ub_write(const char *str, size_t len) {
    write(STDOUT_FILENO, str, len);
    return 0;
}

void phpkd_error(int type, const char *error_msg, ...) {
    (void)type;
    (void)error_msg;
}

void phpkd_log_message(const char *message, int syslog_type) {
    (void)message;
    (void)syslog_type;
}

void phpkd_send_header(sapi_header_struct *sapi_header, void *server_context) {
    (void)sapi_header;
    (void)server_context;
}
