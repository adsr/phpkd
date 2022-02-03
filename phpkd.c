#include <SAPI.h>
#include <php_main.h>
#include <zend_stream.h>
#include <zend_signal.h>
#include <zend_types.h>

#include "phpkd.h"

extern size_t worker_php_ub_write(int worker_id, const char *cstr, size_t len);
extern void worker_php_send_header(int worker_id, char *header, size_t len);

int phpkd_startup(sapi_module_struct *phpkd_module);
size_t phpkd_ub_write(const char *str, size_t len);
void phpkd_error(int type, const char *error_msg, ...);
void phpkd_log_message(const char *message, int syslog_type);
void phpkd_send_header(sapi_header_struct *sapi_header, void *server_context);

ZEND_TLS int worker_id = -1;

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

	NULL,                          /* read post data */
	NULL,                          /* read cookies */

	NULL,                          /* register server variables */
	phpkd_log_message,             /* log message */
	NULL,                          /* get request time */
	NULL,                          /* child terminate */

	STANDARD_SAPI_MODULE_PROPERTIES
};

int phpkd_init() {
    fprintf(stderr, "init here\n");
    php_tsrm_startup();
    zend_signal_startup();
    sapi_startup(&phpkd_module);
    phpkd_module.startup(&phpkd_module);
    SG(options) |= SAPI_OPTION_NO_CHDIR;
    return SUCCESS;
}

int phpkd_request(int id) {
    zend_file_handle fh;

    worker_id = id;

    fprintf(stderr, "here with id=%d\n", id);

    ts_resource(0);

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
    return worker_php_ub_write(worker_id, str, len);
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
    (void)server_context;
    if (!sapi_header) {
        return;
    }
    worker_php_send_header(worker_id, sapi_header->header, sapi_header->header_len);
}
