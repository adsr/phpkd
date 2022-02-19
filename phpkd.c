#include <SAPI.h>
#include <php_main.h>
#include <php_variables.h>
#include <zend_stream.h>
#include <zend_signal.h>
#include <zend_types.h>

#include "phpkd.h"

extern size_t worker_php_ub_write(int worker_id, const char *cstr, size_t len);
extern void worker_php_send_header(int worker_id, char *header, size_t len);
extern void worker_php_log_message(int worker_id, const char *message, int syslog_type);
extern void worker_php_read_post(int worker_id, size_t nbytes, void **readbuf, size_t *nread);

static void phpkd_process_headers(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata);
static int phpkd_startup(sapi_module_struct *sapi_module);
static size_t phpkd_ub_write(const char *str, size_t len);
static void phpkd_error(int type, const char *error_msg, ...);
static void phpkd_log_message(const char *message, int syslog_type);
static void phpkd_send_header(sapi_header_struct *sapi_header, void *server_context);
static size_t phpkd_read_post(char *writebuf, size_t nbytes);
static char *phpkd_read_cookies();
static void phpkd_register_variables(zval *arr);
static void phpkd_register_variables_cb(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata);
static void phpkd_process_key_val_data(uint8_t *data, void (*cb)(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata), void *udata);

static const char *php_script_path = NULL;
static const char phpkd_ini_entries[] = "";

ZEND_TLS int thread_worker_id = -1;
ZEND_TLS void *thread_svar_data = NULL;

sapi_module_struct phpkd_module = {
    "phpkd",                       /* name */
    "PHP Prefork Daemon",          /* pretty_name */

    phpkd_startup,                 /* startup */
    php_module_shutdown_wrapper,   /* shutdown */

    NULL,                          /* activate */
    NULL,                          /* deactivate */

    phpkd_ub_write,                /* ub_write */
    NULL,                          /* flush */
    NULL,                          /* get_stat */
    NULL,                          /* getenv */

    phpkd_error,                   /* sapi_error */

    NULL,                          /* header_handler */
    NULL,                          /* send_headers */
    phpkd_send_header,             /* send_header */

    phpkd_read_post,               /* read_post */
    phpkd_read_cookies,            /* read_cookies */

    phpkd_register_variables,      /* register_server_variables */
    phpkd_log_message,             /* log_message */
    NULL,                          /* get_request_time */
    NULL,                          /* terminate_process */

    STANDARD_SAPI_MODULE_PROPERTIES
};

int phpkd_init(const char *script_path) {
    php_script_path = script_path;

    php_tsrm_startup();
    zend_signal_startup();
    sapi_startup(&phpkd_module);

    phpkd_module.ini_entries = malloc(sizeof(phpkd_ini_entries));
    memcpy(phpkd_module.ini_entries, phpkd_ini_entries, sizeof(phpkd_ini_entries));

    phpkd_module.startup(&phpkd_module);

    SG(options) |= SAPI_OPTION_NO_CHDIR;

    return SUCCESS;
}

int phpkd_deinit() {
    // php_module_shutdown(); // TODO This segfaults for some reason
    sapi_shutdown();
    tsrm_shutdown();
    free(phpkd_module.ini_entries);
    free((char*)php_script_path);
    return SUCCESS;
}

int phpkd_request(int id, int proto_num, const char *method, char *uri, char *query, void *header_data, void *svar_data) {
    zend_file_handle fh;

    thread_worker_id = id;
    thread_svar_data = svar_data;

    ts_resource(0);

    zend_first_try {
        SG(server_context) = (void*)&thread_worker_id;
        SG(sapi_headers).http_response_code = 200;
        SG(request_info).proto_num = proto_num;
        SG(request_info).request_method = method;
        SG(request_info).request_uri = uri;
        SG(request_info).query_string = query;
        phpkd_process_key_val_data(header_data, phpkd_process_headers, NULL);

        php_request_startup();

        zend_stream_init_filename(&fh, php_script_path);
        php_execute_script(&fh);

        zend_destroy_file_handle(&fh);

        php_request_shutdown(NULL);
    } zend_end_try();

    thread_worker_id = -1;
    thread_svar_data = NULL;

    free((char*)method);
    free((char*)uri);
    free((char*)query);
    free(header_data);
    free(svar_data);

    return SUCCESS;
}

static void phpkd_process_headers(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata) {
    (void)nval;
    (void)udata;
    if (strncasecmp(key, "content-type", nkey) == 0) {
        SG(request_info).content_type = val;
    } else if (strncasecmp(key, "content-length", nkey) == 0) {
        SG(request_info).content_length = ZEND_ATOL(val);
    } else if (strncasecmp(key, "cookie", nkey) == 0) {
        SG(request_info).cookie_data = (char*)val;
    } else if (strncasecmp(key, "authorization", nkey) == 0) {
        php_handle_auth_data(val);
    }
}

static int phpkd_startup(sapi_module_struct *sapi_module) {
    return php_module_startup(sapi_module, NULL, 0);
}

static size_t phpkd_ub_write(const char *str, size_t len) {
    return worker_php_ub_write(thread_worker_id, str, len);
}

static void phpkd_error(int type, const char *error_msg, ...) {
    char buf[4096];
    va_list ap;
    va_start(ap, error_msg);
    vsnprintf(buf, sizeof(buf), error_msg, ap);
    va_end(ap);
    (void)type;
    phpkd_log_message(buf, LOG_ERR);
}

static void phpkd_log_message(const char *message, int syslog_type) {
    worker_php_log_message(thread_worker_id, message, syslog_type);
}

static void phpkd_send_header(sapi_header_struct *sapi_header, void *server_context) {
    (void)server_context;
    if (!sapi_header) {
        return;
    }
    worker_php_send_header(thread_worker_id, sapi_header->header, sapi_header->header_len);
}

static size_t phpkd_read_post(char *writebuf, size_t nbytes) {
    char *readbuf = NULL;
    size_t nread = 0;
    worker_php_read_post(thread_worker_id, nbytes, (void **)&readbuf, &nread);
    memcpy(writebuf, readbuf, nread);
    free(readbuf);
    return nread;
}

static char *phpkd_read_cookies() {
    return SG(request_info).cookie_data;
}

static void phpkd_register_variables(zval *arr) {
    phpkd_process_key_val_data(thread_svar_data, phpkd_register_variables_cb, arr);
}

static void phpkd_register_variables_cb(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata) {
    (void)nkey;
    zval *arr = udata;
    php_register_variable_safe(key, val, nval - 1, arr);
}

static void phpkd_process_key_val_data(uint8_t *data, void (*cb)(const char *key, uint64_t nkey, const char *val, uint64_t nval, void *udata), void *udata) {
    size_t cursor = 0;

    uint64_t npairs = *((uint64_t*)(data + cursor));
    cursor += sizeof(uint64_t);

    uint64_t i;
    for (i = 0; i < npairs; i++) {
        const char *key, *val;
        uint64_t nkey, nval;

        nkey = *((uint64_t*)(data + cursor));
        cursor += sizeof(uint64_t);

        key = (const char*)(data + cursor);
        cursor += nkey;

        nval = *((uint64_t*)(data + cursor));
        cursor += sizeof(uint64_t);

        val = (const char*)(data + cursor);
        cursor += nval;

        cb(key, nkey, val, nval, udata);
    }
}
