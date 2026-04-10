// Copyright 2026 Pasqal and its contributors
// SPDX-License-Identifier: Apache-2.0

#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "qrmi.h"

#define DEFAULT_QRMI_CONFIG_PATH "/etc/slurm/qrmi_config.json"
#define DEFAULT_RESOURCE_NAME "qpu"
#define METADATA_FILENAME "qrmi_ocs_acquired.tsv"

typedef struct {
    char *name;
    QrmiResourceType type;
    char *token;
    long acquired_epoch;
} AcquiredResource;

static void log_line(FILE *stream, const char *level, const char *fmt, ...) {
    va_list args;
    va_start(args, fmt);
    fprintf(stream, "qrmi-ocs-prolog[%s]: ", level);
    vfprintf(stream, fmt, args);
    fprintf(stream, "\n");
    va_end(args);
}

static char *dup_text(const char *src) {
    size_t len;
    char *out;

    if (src == NULL) {
        return NULL;
    }
    len = strlen(src);
    out = (char *)malloc(len + 1);
    if (out == NULL) {
        return NULL;
    }
    memcpy(out, src, len + 1);
    return out;
}

static char *trim_token(char *token) {
    char *start = token;
    char *end;

    if (token == NULL) {
        return token;
    }

    while (*start != '\0' &&
           (isspace((unsigned char)*start) || *start == '[' || *start == '(' ||
            *start == '{' || *start == ',' || *start == ';')) {
        start++;
    }

    end = start + strlen(start);
    while (end > start) {
        char c = *(end - 1);
        if (isspace((unsigned char)c) || c == ']' || c == ')' || c == '}' ||
            c == ',' || c == ';') {
            end--;
            continue;
        }
        break;
    }
    *end = '\0';

    return start;
}

static bool text_equals_ignore_case_span(const char *text, size_t text_len, const char *literal) {
    size_t i;
    size_t literal_len;

    if (text == NULL || literal == NULL) {
        return false;
    }
    literal_len = strlen(literal);
    if (text_len != literal_len) {
        return false;
    }

    for (i = 0; i < text_len; i++) {
        if (tolower((unsigned char)text[i]) != tolower((unsigned char)literal[i])) {
            return false;
        }
    }
    return true;
}

static const char *map_debug_level(long level) {
    if (level <= 2) {
        return "error";
    }
    if (level == 3) {
        return "info";
    }
    if (level == 4) {
        return "debug";
    }
    if (level >= 5) {
        return "trace";
    }
    return "info";
}

static const char *resolve_rust_log_level(const char *raw) {
    const char *start;
    const char *end;
    size_t len;
    char number_buf[32];
    char *parse_end;
    long parsed;

    if (raw == NULL) {
        return NULL;
    }

    start = raw;
    while (*start != '\0' && isspace((unsigned char)*start)) {
        start++;
    }
    end = start + strlen(start);
    while (end > start && isspace((unsigned char)*(end - 1))) {
        end--;
    }
    len = (size_t)(end - start);
    if (len == 0) {
        return NULL;
    }

    if (len < sizeof(number_buf)) {
        memcpy(number_buf, start, len);
        number_buf[len] = '\0';
        errno = 0;
        parsed = strtol(number_buf, &parse_end, 10);
        if (errno == 0 && parse_end != number_buf && *parse_end == '\0') {
            return map_debug_level(parsed);
        }
    }

    if (text_equals_ignore_case_span(start, len, "error")) {
        return "error";
    }
    if (text_equals_ignore_case_span(start, len, "warn")) {
        return "warn";
    }
    if (text_equals_ignore_case_span(start, len, "info")) {
        return "info";
    }
    if (text_equals_ignore_case_span(start, len, "debug")) {
        return "debug";
    }
    if (text_equals_ignore_case_span(start, len, "trace")) {
        return "trace";
    }
    return NULL;
}

/* parse_granted_backend parses scheduler grant values in the current
 * single-backend model and returns one backend name.
 */
static int parse_granted_backend(const char *granted, char **backend_name) {
    char *copy;
    char *normalized;
    char *eq;

    if (granted == NULL || *granted == '\0' || backend_name == NULL) {
        return -1;
    }

    copy = dup_text(granted);
    if (copy == NULL) {
        return -1;
    }

    normalized = trim_token(copy);
    if (*normalized == '\0') {
        free(copy);
        return -1;
    }

    if (strchr(normalized, '(') != NULL || strchr(normalized, ')') != NULL) {
        free(copy);
        return -1;
    }

    eq = strchr(normalized, '=');
    if (eq != NULL) {
        normalized = trim_token(eq + 1);
    }
    if (*normalized == '\0') {
        free(copy);
        return -1;
    }
    if (strpbrk(normalized, ", \t\r\n") != NULL) {
        free(copy);
        return -1;
    }

    *backend_name = dup_text(normalized);
    free(copy);
    return *backend_name == NULL ? -1 : 0;
}

static int resolve_job_env_path(char *path, size_t size) {
    const char *job_env = getenv("SGE_JOB_ENV");
    const char *spool;

    if (job_env != NULL && *job_env != '\0') {
        if (snprintf(path, size, "%s", job_env) >= (int)size) {
            return -1;
        }
        return 0;
    }

    spool = getenv("SGE_JOB_SPOOL_DIR");
    if (spool == NULL || *spool == '\0') {
        return -1;
    }

    if (snprintf(path, size, "%s/environment", spool) >= (int)size) {
        return -1;
    }
    return 0;
}

/* resolve_metadata_path chooses where prolog writes acquisition records for
 * epilog release. Priority: explicit env override, job spool dir, then /tmp.
 */
static int resolve_metadata_path(char *path, size_t size) {
    const char *forced = getenv("QRMI_OCS_METADATA_PATH");
    const char *spool;
    const char *job_id;

    if (forced != NULL && *forced != '\0') {
        if (snprintf(path, size, "%s", forced) >= (int)size) {
            return -1;
        }
        return 0;
    }

    spool = getenv("SGE_JOB_SPOOL_DIR");
    if (spool != NULL && *spool != '\0') {
        if (snprintf(path, size, "%s/%s", spool, METADATA_FILENAME) >= (int)size) {
            return -1;
        }
        return 0;
    }

    job_id = getenv("JOB_ID");
    if (job_id != NULL && *job_id != '\0') {
        if (snprintf(path, size, "/tmp/qrmi_ocs_%s.tsv", job_id) >= (int)size) {
            return -1;
        }
        return 0;
    }

    if (snprintf(path, size, "/tmp/qrmi_ocs_acquired.tsv") >= (int)size) {
        return -1;
    }
    return 0;
}

/* set_runtime_env updates the current process env and optionally appends the
 * same key/value into the scheduler job environment file.
 */
static int set_runtime_env(FILE *job_env, const char *key, const char *value, bool export_to_job) {
    if (setenv(key, value, 1) != 0) {
        log_line(stderr, "ERROR", "setenv(%s) failed: %s", key, strerror(errno));
        return -1;
    }

    if (export_to_job && job_env != NULL) {
        if (fprintf(job_env, "%s=%s\n", key, value) < 0) {
            log_line(stderr,
                     "ERROR",
                     "failed writing %s to job environment file: %s",
                     key,
                     strerror(errno));
            return -1;
        }
        fflush(job_env);
    }

    return 0;
}

/* apply_default_rust_log maps scheduler/admin debug levels to RUST_LOG only
 * when RUST_LOG is not already set by submit/admin configuration.
 */
static int apply_default_rust_log(FILE *job_env) {
    const char *current = getenv("RUST_LOG");
    const char *candidate = NULL;
    const char *level = NULL;

    if (current != NULL && *current != '\0') {
        return 0;
    }

    candidate = getenv("QRMI_OCS_LOG_LEVEL");
    if (candidate == NULL || *candidate == '\0') {
        candidate = getenv("SGE_DEBUG_LEVEL");
    }
    level = resolve_rust_log_level(candidate);
    if (level == NULL) {
        return 0;
    }

    return set_runtime_env(job_env, "RUST_LOG", level, true);
}

// Same trick as in spank. We want to log plugin errors to the job environment so they are visible in the job, epilog and accounting, but we also want to log them to stderr for visibility in prolog logs. This helper sets the env vars and logs the message.
static int set_plugin_error(FILE *job_env, const char *message) {
    int rc = 0;

    if (message == NULL || *message == '\0') {
        message = "unknown qrmi-ocs-prolog error";
    }

    rc |= set_runtime_env(job_env, "QRMI_PLUGIN_ERROR", message, true);
    rc |= set_runtime_env(job_env, "qrmi_prolog_status", "error", true);
    return rc == 0 ? 0 : -1;
}

static char *copy_qrmi_error(void) {
    const char *raw = qrmi_get_last_error();

    if (raw == NULL || *raw == '\0') {
        return dup_text("QRMI returned an unspecified error");
    }
    return dup_text(raw);
}

static int apply_backend_env(FILE *job_env,
                             const char *backend,
                             QrmiEnvironmentVariables env_vars) {
    /* Export backend-prefixed values from qrmi_config.json into runtime/job env. */
    size_t i;
    for (i = 0; i < env_vars.length; i++) {
        char env_name[512];
        const char *configured_key = env_vars.variables[i].key;
        const char *configured_value = env_vars.variables[i].value;

        if (configured_key == NULL || configured_key[0] == '\0') {
            continue;
        }
        if (snprintf(env_name, sizeof(env_name), "%s_%s", backend, configured_key) >=
            (int)sizeof(env_name)) {
            log_line(stderr,
                     "ERROR",
                     "environment variable name too long for backend %s key %s",
                     backend,
                     configured_key);
            return -1;
        }

        if (configured_value == NULL) {
            continue;
        }

        if (set_runtime_env(job_env, env_name, configured_value, true) != 0) {
            return -1;
        }
    }

    return 0;
}

/* release_acquired_resources is used on failure paths in prolog so partially
 * acquired tokens are not leaked when setup aborts before metadata write.
 */
static void release_acquired_resources(AcquiredResource *resources, size_t count) {
    size_t i;
    for (i = 0; i < count; i++) {
        QrmiQuantumResource *qrmi = qrmi_resource_new(resources[i].name, resources[i].type);
        if (qrmi == NULL) {
            continue;
        }
        (void)qrmi_resource_release(qrmi, resources[i].token);
        (void)qrmi_resource_free(qrmi);
    }
}

static void free_acquired_resources(AcquiredResource *resources, size_t count) {
    size_t i;

    if (resources == NULL) {
        return;
    }
    for (i = 0; i < count; i++) {
        free(resources[i].name);
        free(resources[i].token);
    }
    free(resources);
}

int main(void) {
    const char *config_path;
    const char *resource_name;
    char hard_grant_env[256];
    const char *granted = NULL;
    char *backend_name = NULL;

    QrmiConfig *config = NULL;

    char job_env_path[PATH_MAX];
    FILE *job_env_file = NULL;

    AcquiredResource *acquired = NULL;
    size_t acquired_count = 0;

    char *resource_csv = NULL;
    char *type_csv = NULL;

    char metadata_path[PATH_MAX];
    FILE *metadata_file = NULL;
    char count_buf[32];

    size_t i;
    config_path = getenv("QRMI_OCS_CONFIG_PATH");
    if (config_path == NULL || *config_path == '\0') {
        config_path = DEFAULT_QRMI_CONFIG_PATH;
    }

    resource_name = getenv("QRMI_OCS_RESOURCE_NAME");
    if (resource_name == NULL || *resource_name == '\0') {
        resource_name = DEFAULT_RESOURCE_NAME;
    }

    if (snprintf(hard_grant_env, sizeof(hard_grant_env), "SGE_HGR_%s", resource_name) >=
        (int)sizeof(hard_grant_env)) {
        log_line(stderr, "ERROR", "resource name too long: %s", resource_name);
        return 1;
    }

    granted = getenv(hard_grant_env);
    if (granted == NULL || *granted == '\0') {
        if (snprintf(hard_grant_env, sizeof(hard_grant_env), "SGE_SGR_%s", resource_name) >=
            (int)sizeof(hard_grant_env)) {
                log_line(stderr, "ERROR", "resource name too long: %s", resource_name);
                return 1;
            }
            granted = getenv(hard_grant_env);
    }

    if (granted == NULL || *granted == '\0') {
        log_line(stderr,
                 "ERROR",
                 "no granted value found in SGE_HGR_%s or SGE_SGR_%s",
                 resource_name,
                 resource_name);
        return 1;
    }

    if (parse_granted_backend(granted, &backend_name) != 0) {
        log_line(stderr, "ERROR", "failed to parse granted resource value: %s", granted);
        free(backend_name);
        return 1;
    }

    if (resolve_job_env_path(job_env_path, sizeof(job_env_path)) == 0) {
        job_env_file = fopen(job_env_path, "a");
        if (job_env_file == NULL) {
            log_line(stderr,
                     "ERROR",
                     "failed to open job environment file %s: %s",
                     job_env_path,
                     strerror(errno));
            free(backend_name);
            return 1;
        }
    } else {
        log_line(stderr,
                 "ERROR",
                 "unable to resolve job environment file path (SGE_JOB_ENV or SGE_JOB_SPOOL_DIR) ");
        free(backend_name);
        return 1;
    }

    /* Apply default log level before loading QRMI config so downstream operations use it. */
    if (apply_default_rust_log(job_env_file) != 0) {
        set_plugin_error(job_env_file, "failed to set RUST_LOG");
        goto fail;
    }

    config = qrmi_config_load(config_path);
    if (config == NULL) {
        char *err = copy_qrmi_error();
        set_plugin_error(job_env_file, err);
        log_line(stderr, "ERROR", "failed to load config %s: %s", config_path, err);
        free(err);
        goto fail;
    }

    /* Single-backend acquisition flow:
     * - resolve backend from granted resource
     * - apply backend-prefixed env
     * - acquire token
     * - export runtime/accounting env
     */
    {
        const char *backend = backend_name;
        QrmiResourceDef *resource_def = qrmi_config_resource_def_get(config, backend);
        QrmiQuantumResource *qrmi = NULL;
        bool accessible = false;
        char *token_raw = NULL;
        char *token_copy = NULL;
        char *error_text = NULL;
        const char *type_raw;
        AcquiredResource *next;
        char token_env_name[512];

        if (resource_def == NULL) {
            error_text = copy_qrmi_error();
            if (error_text == NULL) {
                error_text = dup_text("resource missing from config");
            }
            set_plugin_error(job_env_file, error_text);
            log_line(stderr,
                     "ERROR",
                     "resource %s not found in %s: %s",
                     backend,
                     config_path,
                     error_text);
            free(error_text);
            goto fail;
        }

        if (apply_backend_env(job_env_file, backend, resource_def->environments) != 0) {
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to apply backend environment variables");
            goto fail;
        }

        qrmi = qrmi_resource_new(backend, resource_def->type);
        if (qrmi == NULL) {
            error_text = copy_qrmi_error();
            set_plugin_error(job_env_file, error_text);
            log_line(stderr,
                     "ERROR",
                     "qrmi_resource_new failed for %s: %s",
                     backend,
                     error_text);
            free(error_text);
            qrmi_config_resource_def_free(resource_def);
            goto fail;
        }

        if (qrmi_resource_is_accessible(qrmi, &accessible) != QRMI_RETURN_CODE_SUCCESS || !accessible) {
            error_text = copy_qrmi_error();
            set_plugin_error(job_env_file, error_text);
            log_line(stderr,
                     "ERROR",
                     "backend %s is not accessible: %s",
                     backend,
                     error_text);
            free(error_text);
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            goto fail;
        }

        if (qrmi_resource_acquire(qrmi, &token_raw) != QRMI_RETURN_CODE_SUCCESS || token_raw == NULL) {
            error_text = copy_qrmi_error();
            set_plugin_error(job_env_file, error_text);
            log_line(stderr,
                     "ERROR",
                     "qrmi_resource_acquire failed for %s: %s",
                     backend,
                     error_text);
            free(error_text);
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            goto fail;
        }

        token_copy = dup_text(token_raw);
        if (token_copy == NULL) {
            qrmi_string_free(token_raw);
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to copy acquisition token");
            goto fail;
        }
        qrmi_string_free(token_raw);
        token_raw = NULL;

        next = (AcquiredResource *)realloc(acquired, sizeof(AcquiredResource) * (acquired_count + 1));
        if (next == NULL) {
            free(token_copy);
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to allocate acquisition record");
            goto fail;
        }
        acquired = next;
        acquired[acquired_count].name = dup_text(backend);
        acquired[acquired_count].type = resource_def->type;
        acquired[acquired_count].token = token_copy;
        acquired[acquired_count].acquired_epoch = (long)time(NULL);
        if (acquired[acquired_count].name == NULL) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to allocate resource name");
            goto fail;
        }
        acquired_count++;

        if (snprintf(token_env_name,
                     sizeof(token_env_name),
                     "%s_QRMI_JOB_ACQUISITION_TOKEN",
                     backend) >= (int)sizeof(token_env_name)) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "acquisition token env name too long");
            goto fail;
        }
        if (set_runtime_env(job_env_file, token_env_name, token_copy, true) != 0) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to export acquisition token");
            goto fail;
        }

        resource_csv = dup_text(backend);
        if (resource_csv == NULL) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to build backend list");
            goto fail;
        }

        type_raw = qrmi_config_resource_type_to_str(resource_def->type);
        if (type_raw == NULL) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to resolve resource type string");
            goto fail;
        }
        type_csv = dup_text(type_raw);
        if (type_csv == NULL) {
            qrmi_resource_free(qrmi);
            qrmi_config_resource_def_free(resource_def);
            set_plugin_error(job_env_file, "failed to build type list");
            qrmi_string_free((char *)type_raw);
            goto fail;
        }
        qrmi_string_free((char *)type_raw);

        qrmi_resource_free(qrmi);
        qrmi_config_resource_def_free(resource_def);
    }

    if (acquired_count == 0) {
        set_plugin_error(job_env_file, "no resources acquired");
        goto fail;
    }

    if (set_runtime_env(job_env_file, "SLURM_JOB_QPU_RESOURCES", resource_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export SLURM_JOB_QPU_RESOURCES");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "SLURM_JOB_QPU_TYPES", type_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export SLURM_JOB_QPU_TYPES");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "QRMI_JOB_QPU_RESOURCES", resource_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export QRMI_JOB_QPU_RESOURCES");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "QRMI_JOB_QPU_TYPES", type_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export QRMI_JOB_QPU_TYPES");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "qrmi_resources", resource_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export qrmi_resources");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "qrmi_resource_types", type_csv, true) != 0) {
        set_plugin_error(job_env_file, "failed to export qrmi_resource_types");
        goto fail;
    }
    if (snprintf(count_buf, sizeof(count_buf), "%zu", acquired_count) >= (int)sizeof(count_buf)) {
        set_plugin_error(job_env_file, "failed to format acquired resource count");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "qrmi_acquired_count", count_buf, true) != 0) {
        set_plugin_error(job_env_file, "failed to export qrmi_acquired_count");
        goto fail;
    }
    if (set_runtime_env(job_env_file, "qrmi_prolog_status", "success", true) != 0) {
        set_plugin_error(job_env_file, "failed to export qrmi_prolog_status");
        goto fail;
    }

    if (resolve_metadata_path(metadata_path, sizeof(metadata_path)) != 0) {
        set_plugin_error(job_env_file, "failed to resolve metadata path");
        goto fail;
    }

    metadata_file = fopen(metadata_path, "w");
    if (metadata_file == NULL) {
        set_plugin_error(job_env_file, "failed to open metadata file");
        log_line(stderr,
                 "ERROR",
                 "failed to open metadata file %s: %s",
                 metadata_path,
                 strerror(errno));
        goto fail;
    }

    for (i = 0; i < acquired_count; i++) {
        if (fprintf(metadata_file,
                    "%s\t%d\t%s\t%ld\n",
                    acquired[i].name,
                    (int)acquired[i].type,
                    acquired[i].token,
                    acquired[i].acquired_epoch) < 0) {
            set_plugin_error(job_env_file, "failed to write metadata file");
            fclose(metadata_file);
            metadata_file = NULL;
            goto fail;
        }
    }
    fclose(metadata_file);
    metadata_file = NULL;

    if (set_runtime_env(job_env_file, "QRMI_OCS_METADATA_PATH", metadata_path, true) != 0) {
        set_plugin_error(job_env_file, "failed to export metadata path");
        goto fail;
    }

    log_line(stdout,
             "INFO",
             "acquired %zu backend resource(s): %s",
             acquired_count,
             resource_csv == NULL ? "" : resource_csv);

    free(resource_csv);
    free(type_csv);
    free(backend_name);
    free_acquired_resources(acquired, acquired_count);
    qrmi_config_free(config);
    fclose(job_env_file);
    return 0;

fail:
    if (metadata_file != NULL) {
        fclose(metadata_file);
    }
    release_acquired_resources(acquired, acquired_count);
    free(resource_csv);
    free(type_csv);
    free(backend_name);
    free_acquired_resources(acquired, acquired_count);
    if (config != NULL) {
        qrmi_config_free(config);
    }
    if (job_env_file != NULL) {
        fclose(job_env_file);
    }

    return 1;
}
