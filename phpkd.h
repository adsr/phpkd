#ifndef PHPKD_H
#define PHPKD_H

int phpkd_init(const char *script_path);
int phpkd_request(int id, int proto_num, const char *method, char *uri, char *query, void *header_data, void *svar_data);
int phpkd_deinit();

#endif
