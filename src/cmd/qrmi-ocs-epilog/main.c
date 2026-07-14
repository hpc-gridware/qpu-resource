// Copyright 2026 Pasqal and its contributors
// SPDX-License-Identifier: Apache-2.0

#define _GNU_SOURCE

#include <errno.h>
#include <limits.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#include "qrmi.h"

#define METADATA_FILENAME "qrmi_ocs_acquired.tsv"

typedef struct {
    char *name;
    QrmiResourceType type;
    char *token;
    long acquired_epoch;
} AcquiredRecord;

static void log_line(FILE *stream, const char *level, const char *fmt, ...) {
    va_list args;
    va_start(args, fmt);
    fprintf(stream, "qrmi-ocs-epilog[%s]: ", level);
    vfprintf(stream, fmt, args);
    fprintf(stream, "\n");
    va_end(args);
}

#if defined(QRMI_VERSION) && QRMI_VERSION >= QRMI_VERSION_NUMERIC(0,18,0)
static void log_qrmi_line(const char *level, const char *target, const char *message) {
    const char *log_level = level == NULL ? "INFO" : level;
    const char *log_target = target == NULL ? "qrmi" : target;
    const char *log_message = message == NULL ? "" : message;
    FILE *stream = strcmp(log_level, "ERROR") == 0 || strcmp(log_level, "WARN") == 0 ? stderr : stdout;

    log_line(stream, log_level, "QRMI %s: %s", log_target, log_message);
}
#endif

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

static int resolve_usage_path(char *path, size_t size) {
    const char *spool = getenv("SGE_JOB_SPOOL_DIR");

    if (spool == NULL || *spool == '\0') {
        return -1;
    }
    if (snprintf(path, size, "%s/usage", spool) >= (int)size) {
        return -1;
    }
    return 0;
}

// prepares runtime env for the epilog.
static int set_runtime_env(FILE *job_env, const char *key, const char *value, bool export_to_job) {
    if (setenv(key, value, 1) != 0) {
        log_line(stderr, "ERROR", "setenv(%s) failed: %s", key, strerror(errno));
        return -1;
    }

    // TODO: epilog only runs after job, so I don't think this is needed?
    if (export_to_job && job_env != NULL) {
        if (fprintf(job_env, "%s=%s\n", key, value) < 0) {
            log_line(stderr, "ERROR", "failed writing %s to job env file: %s", key, strerror(errno));
            return -1;
        }
        fflush(job_env);
    }
    return 0;
}

/* append_usage_metric appends a key=value line to the usage file to get accounting metrics. */
static int append_usage_metric(const char *usage_path, const char *key, const char *value) {
    FILE *usage;

    if (usage_path == NULL || key == NULL || value == NULL) {
        return -1;
    }

    usage = fopen(usage_path, "a");
    if (usage == NULL) {
        log_line(stderr, "WARN", "failed to open usage file %s: %s", usage_path, strerror(errno));
        return -1;
    }
    if (fprintf(usage, "%s=%s\n", key, value) < 0) {
        log_line(stderr, "WARN", "failed writing %s to usage file %s: %s", key, usage_path, strerror(errno));
        fclose(usage);
        return -1;
    }
    fclose(usage);
    return 0;
}

static void free_record(AcquiredRecord *record) {
    if (record == NULL) {
        return;
    }
    free(record->name);
    record->name = NULL;
    free(record->token);
    record->token = NULL;
}

/* parse_record_line parses one metadata TSV line written by prolog:
 * name <tab> type <tab> token <tab> acquired_epoch
 */
static int parse_record_line(char *line, AcquiredRecord *record) {
    char *state = NULL;
    char *name;
    char *type_text;
    char *type_end = NULL;
    char *token;
    char *epoch_text;
    char *epoch_end = NULL;
    char *line_end;
    long parsed_type;
    long parsed_epoch;

    if (line == NULL || record == NULL) {
        return -1;
    }

    line_end = line + strcspn(line, "\r\n");
    *line_end = '\0';

    name = strtok_r(line, "\t", &state);
    type_text = strtok_r(NULL, "\t", &state);
    token = strtok_r(NULL, "\t", &state);
    epoch_text = strtok_r(NULL, "\t", &state);

    if (name == NULL || type_text == NULL || token == NULL || *name == '\0' || *type_text == '\0' ||
        *token == '\0') {
        return -1;
    }

    errno = 0;
    parsed_type = strtol(type_text, &type_end, 10);
    if (errno != 0 || type_end == type_text || *type_end != '\0' || parsed_type < 0 ||
        parsed_type > INT_MAX) {
        return -1;
    }

    parsed_epoch = 0;
    if (epoch_text != NULL && *epoch_text != '\0') {
        errno = 0;
        parsed_epoch = strtol(epoch_text, &epoch_end, 10);
        if (errno != 0 || epoch_end == epoch_text || *epoch_end != '\0') {
            return -1;
        }
    }

    record->name = dup_text(name);
    record->type = (QrmiResourceType)parsed_type;
    record->token = dup_text(token);
    record->acquired_epoch = parsed_epoch;
    if (record->name == NULL || record->token == NULL) {
        free_record(record);
        return -1;
    }
    return 0;
}

/* release_record releases exactly one acquired token described by metadata. */
static int release_record(const AcquiredRecord *record) {
    QrmiQuantumResource *qrmi;
    QrmiReturnCode rc;

    if (record == NULL) {
        return -1;
    }

    qrmi = qrmi_resource_new(record->name, record->type);
    if (qrmi == NULL) {
        const char *raw = qrmi_get_last_error();
        log_line(stderr, "ERROR", "qrmi_resource_new(%s) failed: %s", record->name, raw == NULL ? "" : raw);
        return -1;
    }

    rc = qrmi_resource_release(qrmi, record->token);
    if (rc != QRMI_RETURN_CODE_SUCCESS) {
        const char *raw = qrmi_get_last_error();
        log_line(stderr,
                 "ERROR",
                 "qrmi_resource_release(%s, token=...) failed: %s",
                 record->name,
                 raw == NULL ? "" : raw);
        qrmi_resource_free(qrmi);
        return -1;
    }

    qrmi_resource_free(qrmi);
    return 0;
}

int main(void) {
    char metadata_path[PATH_MAX];
    char job_env_path[PATH_MAX];
    char usage_path[PATH_MAX];
    FILE *metadata = NULL;
    FILE *job_env = NULL;
    char *line = NULL;
    size_t line_cap = 0;
    ssize_t line_len;
    AcquiredRecord record;
    bool have_record = false;
    bool extra_record = false;
    size_t total = 0;
    size_t released = 0;
    size_t failed = 0;
    time_t start_ts = time(NULL);
    long elapsed = 0;
    char numbuf[32];

#if defined(QRMI_HAS_LOG_CALLBACK)
    qrmi_log_callback_set(log_qrmi_line);
#endif

    if (resolve_metadata_path(metadata_path, sizeof(metadata_path)) != 0) {
        log_line(stderr, "ERROR", "failed to resolve metadata path");
        return 1;
    }

    metadata = fopen(metadata_path, "r");
    if (metadata == NULL) {
        if (errno == ENOENT) {
            log_line(stdout, "INFO", "no metadata file found at %s; skipping release", metadata_path);
            return 0;
        }
        log_line(stderr, "ERROR", "failed to open metadata file %s: %s", metadata_path, strerror(errno));
        return 1;
    }

    if (resolve_job_env_path(job_env_path, sizeof(job_env_path)) == 0) {
        job_env = fopen(job_env_path, "a");
        if (job_env == NULL) {
            log_line(stderr, "WARN", "failed to open job env file %s: %s", job_env_path, strerror(errno));
        }
    }

    memset(&record, 0, sizeof(record));
    while ((line_len = getline(&line, &line_cap, metadata)) >= 0) {
        if (line_len == 0 || line[0] == '\n' || line[0] == '\r') {
            continue;
        }
        if (!have_record) {
            total = 1;
            if (parse_record_line(line, &record) != 0) {
                failed = 1;
                log_line(stderr, "ERROR", "failed to parse metadata line: %s", line);
                break;
            }
            have_record = true;
            continue;
        }
        extra_record = true;
        failed = 1;
        log_line(stderr, "ERROR", "metadata file has more than one record; unsupported");
        break;
    }

    if (failed == 0 && have_record) {
        if (release_record(&record) == 0) {
            released = 1;
        } else {
            failed = 1;
        }
    }
    if (failed == 0 && !have_record) {
        failed = 1;
        log_line(stderr, "ERROR", "metadata file does not contain a record");
    }

    free(line);
    free_record(&record);
    fclose(metadata);
    metadata = NULL;
    if (unlink(metadata_path) != 0 && errno != ENOENT) {
        log_line(stderr, "WARN", "failed to remove metadata file %s: %s", metadata_path, strerror(errno));
    }

    elapsed = (long)(time(NULL) - start_ts);

    /* prepare runtime environment for epilog */
    if (snprintf(numbuf, sizeof(numbuf), "%zu", total) < (int)sizeof(numbuf)) {
        (void)set_runtime_env(job_env, "qrmi_release_total", numbuf, true); // TODO: do we want to export these to job env? epilog runs after job, so maybe not needed?
    }
    if (snprintf(numbuf, sizeof(numbuf), "%zu", released) < (int)sizeof(numbuf)) {
        (void)set_runtime_env(job_env, "qrmi_release_success", numbuf, true);
    }
    if (snprintf(numbuf, sizeof(numbuf), "%zu", failed) < (int)sizeof(numbuf)) {
        (void)set_runtime_env(job_env, "qrmi_release_failed", numbuf, true);
    }
    if (snprintf(numbuf, sizeof(numbuf), "%ld", elapsed) < (int)sizeof(numbuf)) {
        (void)set_runtime_env(job_env, "qrmi_release_elapsed_seconds", numbuf, true);
    }
    if (failed == 0) {
        (void)set_runtime_env(job_env, "qrmi_epilog_status", "success", true);
    } else {
        (void)set_runtime_env(job_env, "qrmi_epilog_status", "error", true);
    }

    if (resolve_usage_path(usage_path, sizeof(usage_path)) == 0) {
        if (snprintf(numbuf, sizeof(numbuf), "%zu", total) < (int)sizeof(numbuf)) {
            (void)append_usage_metric(usage_path, "qrmi_acquired_count", numbuf);
            (void)append_usage_metric(usage_path, "qrmi_release_total", numbuf);
        }
        if (snprintf(numbuf, sizeof(numbuf), "%zu", released) < (int)sizeof(numbuf)) {
            (void)append_usage_metric(usage_path, "qrmi_release_success", numbuf);
        }
        if (snprintf(numbuf, sizeof(numbuf), "%zu", failed) < (int)sizeof(numbuf)) {
            (void)append_usage_metric(usage_path, "qrmi_release_failed", numbuf);
        }
        if (snprintf(numbuf, sizeof(numbuf), "%ld", elapsed) < (int)sizeof(numbuf)) {
            (void)append_usage_metric(usage_path, "qrmi_release_elapsed_seconds", numbuf);
        }
        (void)append_usage_metric(usage_path, "qrmi_epilog_status_code", failed == 0 ? "1" : "0");
    }

    if (job_env != NULL) {
        fclose(job_env);
        job_env = NULL;
    }

    log_line(stdout,
             "INFO",
             "release summary: total=%zu success=%zu failed=%zu extra_record=%s elapsed=%lds",
             total,
             released,
             failed,
             extra_record ? "true" : "false",
             elapsed);
    return failed == 0 ? 0 : 1;
}
