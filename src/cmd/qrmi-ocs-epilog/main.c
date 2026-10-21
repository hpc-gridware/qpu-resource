// Copyright 2026 Pasqal and its contributors
// SPDX-License-Identifier: Apache-2.0

#define _GNU_SOURCE

#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <pwd.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#include "qrmi.h"

#define METADATA_FILENAME "qrmi_ocs_acquired.tsv"
#define DEFAULT_QRMI_CONFIG_PATH "/etc/qrmi/qrmi_config.json"
#define TRUSTED_PATH "/usr/sbin:/usr/bin:/sbin:/bin"

#if defined(QRMI_HAS_LOG_CALLBACK) ||                                                \
    (defined(QRMI_VERSION) && defined(QRMI_VERSION_NUMERIC) &&                       \
     QRMI_VERSION >= QRMI_VERSION_NUMERIC(0, 20, 0))
#define QRMI_OCS_HAS_LOG_CALLBACK 1
#endif

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

static FILE *open_append_private(const char *path) {
    int fd = open(path, O_WRONLY | O_CREAT | O_APPEND, 0600);
    if (fd < 0) {
        return NULL;
    }
    if (fchmod(fd, 0600) != 0) {
        int chmod_errno = errno;
        close(fd);
        errno = chmod_errno;
        return NULL;
    }
    FILE *file = fdopen(fd, "a");
    if (file == NULL) {
        close(fd);
    }
    return file;
}

#if defined(QRMI_OCS_HAS_LOG_CALLBACK)
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
    const char *spool;

    spool = getenv("SGE_JOB_SPOOL_DIR");
    if (spool == NULL || *spool == '\0' ||
        snprintf(path, size, "%s/%s", spool, METADATA_FILENAME) >= (int)size) {
        return -1;
    }
    return 0;
}

static int resolve_job_env_path(char *path, size_t size) {
    const char *spool;

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

/* The variables OCS sets for the epilog itself; see the prolog. */
static const char *const trusted_env_keys[] = {
    "SGE_ROOT", "SGE_CELL", "SGE_QMASTER_PORT", "SGE_EXECD_PORT",
    "SGE_JOB_SPOOL_DIR", "JOB_ID", "SGE_TASK_ID", "TZ",
};
#define TRUSTED_ENV_COUNT (sizeof(trusted_env_keys) / sizeof(trusted_env_keys[0]))

/* has_loader_env reports dynamic-loader variables. qrmi-ocs-launch removes
 * them before starting the hook; finding one means the hook was started
 * without it, after the loader had already read them.
 */
static bool has_loader_env(void) {
    extern char **environ;
    char **kv;

    for (kv = environ; kv != NULL && *kv != NULL; kv++) {
        if (strncmp(*kv, "LD_", 3) == 0 || strncmp(*kv, "GLIBC_TUNABLES=", 15) == 0) {
            return true;
        }
    }
    return false;
}

/* reset_process_env keeps only the OCS-set variables and sets a fixed PATH,
 * the hook user's HOME and the C locale, so QRMI does not read job-provided
 * settings. qrmi-ocs-launch does the same before the hook starts.
 */
static int reset_process_env(void) {
    char *saved[TRUSTED_ENV_COUNT];
    size_t i;
    int rc = 0;

    for (i = 0; i < TRUSTED_ENV_COUNT; i++) {
        const char *value = getenv(trusted_env_keys[i]);
        saved[i] = value == NULL ? NULL : strdup(value);
    }
    if (clearenv() != 0) {
        rc = -1;
    }
    for (i = 0; i < TRUSTED_ENV_COUNT; i++) {
        if (saved[i] != NULL && setenv(trusted_env_keys[i], saved[i], 1) != 0) {
            rc = -1;
        }
        free(saved[i]);
    }
    {
        struct passwd *account = getpwuid(geteuid());
        const char *home = account != NULL && account->pw_dir != NULL && account->pw_dir[0] == '/'
                               ? account->pw_dir
                               : "/";
        if (setenv("PATH", TRUSTED_PATH, 1) != 0 || setenv("HOME", home, 1) != 0 ||
            setenv("LANG", "C", 1) != 0 || setenv("LC_ALL", "C", 1) != 0) {
            rc = -1;
        }
    }
    return rc;
}

static int set_prefixed_env(const char *prefix, const char *key, const char *value) {
    char name[512];

    if (snprintf(name, sizeof(name), "%s_%s", prefix, key) >= (int)sizeof(name)) {
        return -1;
    }
    return setenv(name, value, 1);
}

/* set_release_env rebuilds the QRMI settings the release needs from
 * qrmi_config.json and the metadata record, as the prolog set them.
 */
static int set_release_env(const char *config_path, const char *job_owner, const AcquiredRecord *record) {
    QrmiConfig *config = qrmi_config_load(config_path);
    QrmiResourceDef *def;
    char uid_buf[32];
    char job_id_buf[512];
    const char *job_id = getenv("JOB_ID");
    const char *task_id = getenv("SGE_TASK_ID");
    uid_t uid = getuid();
    size_t i;
    int rc = 0;

    if (config == NULL) {
        log_line(stderr, "ERROR", "failed to load config %s", config_path);
        return -1;
    }
    def = qrmi_config_resource_def_get(config, record->name);
    if (def == NULL) {
        log_line(stderr, "ERROR", "resource %s not found in %s", record->name, config_path);
        qrmi_config_free(config);
        return -1;
    }
    if (job_owner != NULL && *job_owner != '\0') {
        struct passwd *account = getpwnam(job_owner);
        if (account == NULL) {
            rc = -1;
        } else {
            uid = account->pw_uid;
        }
    }
    if (job_id == NULL) {
        job_id = "";
    }
    if (task_id != NULL && *task_id != '\0' && strcmp(task_id, "undefined") != 0 &&
        strcmp(task_id, "0") != 0) {
        if (snprintf(job_id_buf, sizeof(job_id_buf), "%s.%s", job_id, task_id) >= (int)sizeof(job_id_buf)) {
            rc = -1;
        }
        job_id = job_id_buf;
    }
    if (rc != 0 || snprintf(uid_buf, sizeof(uid_buf), "%u", (unsigned int)uid) >= (int)sizeof(uid_buf) ||
        setenv("QRMI_JOB_UID", uid_buf, 1) != 0 || setenv("QRMI_JOB_ID", job_id, 1) != 0 ||
        set_prefixed_env(record->name, "QRMI_JOB_ACQUISITION_TOKEN", record->token) != 0) {
        rc = -1;
    }
    for (i = 0; rc == 0 && i < def->environments.length; i++) {
        const char *key = def->environments.variables[i].key;
        const char *value = def->environments.variables[i].value;
        if (key != NULL && *key != '\0' && value != NULL &&
            set_prefixed_env(record->name, key, value) != 0) {
            rc = -1;
        }
    }
    qrmi_config_resource_def_free(def);
    qrmi_config_free(config);
    return rc;
}

/* release_record releases exactly one acquired token described by metadata. */
static int release_record(const char *config_path, const char *job_owner, const AcquiredRecord *record) {
    QrmiQuantumResource *qrmi;
    QrmiReturnCode rc;

    if (record == NULL) {
        return -1;
    }
    if (set_release_env(config_path, job_owner, record) != 0) {
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

/* main exits 0 when there is nothing to release or the release was
 * attempted. A failed release is logged, recorded in accounting and leaves
 * the metadata file for administrator recovery: the job has finished, and a
 * non-zero code would put the queue instance into error state. Only a
 * missing spool directory or unreadable metadata exit 1.
 */
int main(int argc, char **argv) {
    const char *config_path = DEFAULT_QRMI_CONFIG_PATH;
    const char *log_level = NULL;
    const char *job_owner = NULL;
    bool metadata_error = false;
    int arg;
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

    if (has_loader_env()) {
        log_line(stderr, "ERROR", "refusing to run: LD_* or GLIBC_TUNABLES is set; start the hook through qrmi-ocs-launch");
        return 100;
    }
    if (reset_process_env() != 0) {
        log_line(stderr, "ERROR", "failed to reset the environment");
        return 1;
    }
    for (arg = 1; arg < argc; arg++) {
        if (strncmp(argv[arg], "--config=", 9) == 0) {
            config_path = argv[arg] + 9;
        } else if (strncmp(argv[arg], "--log-level=", 12) == 0) {
            log_level = argv[arg] + 12;
        } else if (argv[arg][0] == '-' || job_owner != NULL) {
            log_line(stderr, "ERROR", "invalid epilog argument: %s", argv[arg]);
            return 1;
        } else {
            job_owner = argv[arg];
        }
    }
    if (log_level != NULL && *log_level != '\0') {
        /* Same mapping as the prolog: SGE debug levels or RUST_LOG names. */
        static const char *const names[] = {"error", "warn", "info", "debug", "trace"};
        char *end;
        long level = strtol(log_level, &end, 10);
        const char *rust = NULL;
        size_t n;
        if (end != log_level && *end == '\0') {
            rust = level <= 2 ? "error" : level == 3 ? "info" : level == 4 ? "debug" : "trace";
        }
        for (n = 0; rust == NULL && n < sizeof(names) / sizeof(names[0]); n++) {
            if (strcasecmp(log_level, names[n]) == 0) {
                rust = names[n];
            }
        }
        if (rust != NULL) {
            (void)setenv("RUST_LOG", rust, 1);
        }
    }

#if defined(QRMI_OCS_HAS_LOG_CALLBACK)
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
        job_env = open_append_private(job_env_path);
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
                metadata_error = true;
                log_line(stderr, "ERROR", "failed to parse metadata line");
                break;
            }
            have_record = true;
            continue;
        }
        extra_record = true;
        failed = 1;
        metadata_error = true;
        log_line(stderr, "ERROR", "metadata file has more than one record; unsupported");
        break;
    }

    if (failed == 0 && have_record) {
        if (release_record(config_path, job_owner, &record) == 0) {
            released = 1;
        } else {
            failed = 1;
        }
    }
    if (failed == 0 && !have_record) {
        failed = 1;
        metadata_error = true;
        log_line(stderr, "ERROR", "metadata file does not contain a record");
    }

    free(line);
    free_record(&record);
    fclose(metadata);
    metadata = NULL;
    if (failed == 0) {
        if (unlink(metadata_path) != 0 && errno != ENOENT) {
            log_line(stderr, "WARN", "failed to remove metadata file %s: %s", metadata_path, strerror(errno));
        }
    } else {
        log_line(stderr, "WARN", "retaining metadata file %s for administrator recovery", metadata_path);
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
    return metadata_error ? 1 : 0;
}
