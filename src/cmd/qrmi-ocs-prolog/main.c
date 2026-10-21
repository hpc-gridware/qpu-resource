// Copyright 2026 Pasqal and its contributors
// SPDX-License-Identifier: Apache-2.0

#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <pwd.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include "qrmi.h"

#define DEFAULT_QRMI_CONFIG_PATH "/etc/qrmi/qrmi_config.json"
#define DEFAULT_RESOURCE_NAME "qpu"
#define DEFAULT_SLOTS_RESOURCE_NAME "qpu_slots"
#define METADATA_FILENAME "qrmi_ocs_acquired.tsv"
#define TRUSTED_PATH "/usr/sbin:/usr/bin:/sbin:/bin"

/* Exit codes OCS interprets for queue prologs: 99 reschedules the job, 100
 * puts the job into error state, any other non-zero code puts the queue
 * instance into error state.
 */
#define EXIT_HOST_ERROR 1
#define EXIT_RESCHEDULE 99
#define EXIT_JOB_ERROR 100

/* Results of reading scheduler state. */
#define LOOKUP_FOUND 0
#define LOOKUP_NOT_FOUND 1
#define LOOKUP_HOST_ERROR (-1)
#define LOOKUP_RETRY (-2)

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
} AcquiredResource;

static void log_line(FILE *stream, const char *level, const char *fmt, ...) {
    va_list args;
    va_start(args, fmt);
    fprintf(stream, "qrmi-ocs-prolog[%s]: ", level);
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

/* The variables qrmi-ocs-launch sets for the prolog (TZ only when it is a
 * plain time zone name). Everything else in the prolog's environment,
 * including PATH, can come from the job.
 */
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
 * the hook user's HOME and the C locale. The prolog runs as the OCS admin
 * user (root by default) with the job's environment, so QRMI and qstat must
 * not read job-provided values; backend settings come from qrmi_config.json.
 * qrmi-ocs-launch does the same before the hook starts.
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

/* check_trusted_executable requires an absolute path to a root-owned regular
 * file that group and others cannot write. The test harness defines
 * QRMI_OCS_TEST_TRUST_ANY_EXECUTABLE to run fake scheduler commands.
 */
#ifdef QRMI_OCS_TEST_TRUST_ANY_EXECUTABLE
static int check_trusted_executable(const char *path) {
    return path != NULL && path[0] == '/' ? 0 : -1;
}
#else
static int check_trusted_executable(const char *path) {
    char resolved[PATH_MAX];
    struct stat st;

    if (path == NULL || path[0] != '/' || realpath(path, resolved) == NULL ||
        stat(resolved, &st) != 0) {
        return -1;
    }
    if (!S_ISREG(st.st_mode) || st.st_uid != 0 || (st.st_mode & 022) != 0) {
        return -1;
    }
    return 0;
}
#endif

/* run_scheduler_command runs a trusted OCS client command and returns its
 * standard output. LOOKUP_RETRY means the command failed, for example because
 * qmaster was briefly unreachable.
 */
static int run_scheduler_command(const char *path, const char *arg1, const char *arg2, char **out) {
    int output_pipe[2];
    pid_t pid;
    char *buf = NULL;
    size_t len = 0;
    size_t cap = 0;
    char chunk[4096];
    ssize_t got;
    int status;

    *out = NULL;
    if (check_trusted_executable(path) != 0) {
        return LOOKUP_HOST_ERROR;
    }
    if (pipe(output_pipe) != 0) {
        return LOOKUP_HOST_ERROR;
    }
    pid = fork();
    if (pid < 0) {
        close(output_pipe[0]);
        close(output_pipe[1]);
        return LOOKUP_HOST_ERROR;
    }
    if (pid == 0) {
        close(output_pipe[0]);
        if (dup2(output_pipe[1], STDOUT_FILENO) < 0) {
            _exit(127);
        }
        close(output_pipe[1]);
        execl(path, path, arg1, arg2, (char *)NULL);
        _exit(127);
    }
    close(output_pipe[1]);
    while ((got = read(output_pipe[0], chunk, sizeof(chunk))) > 0) {
        if (len + (size_t)got + 1 > cap) {
            size_t next_cap = (len + (size_t)got + 1) * 2;
            char *next = (char *)realloc(buf, next_cap);
            if (next == NULL) {
                free(buf);
                close(output_pipe[0]);
                waitpid(pid, &status, 0);
                return LOOKUP_HOST_ERROR;
            }
            buf = next;
            cap = next_cap;
        }
        memcpy(buf + len, chunk, (size_t)got);
        len += (size_t)got;
    }
    close(output_pipe[0]);
    if (waitpid(pid, &status, 0) < 0 || !WIFEXITED(status) || WEXITSTATUS(status) != 0) {
        free(buf);
        return LOOKUP_RETRY;
    }
    if (buf == NULL) {
        buf = (char *)calloc(1, 1);
        if (buf == NULL) {
            return LOOKUP_HOST_ERROR;
        }
    }
    buf[len] = '\0';
    *out = buf;
    return LOOKUP_FOUND;
}

/* field_value returns the value of the first line starting with field, joined
 * with its continuation lines (indented, or after a trailing backslash), with
 * backslashes removed. A field without a trailing colon, as in qconf output,
 * must be followed by whitespace. The caller frees the result.
 */
static char *field_value(const char *text, const char *field) {
    size_t field_len = strlen(field);
    bool colon = field_len > 0 && field[field_len - 1] == ':';
    const char *line = text;
    char *value = NULL;
    size_t len = 0;
    bool collecting = false;

    while (line != NULL && *line != '\0') {
        const char *end = strchr(line, '\n');
        size_t line_len = end == NULL ? strlen(line) : (size_t)(end - line);
        const char *part = NULL;
        size_t part_len = 0;

        if (!collecting) {
            if (line_len >= field_len && strncmp(line, field, field_len) == 0 &&
                (colon || line_len == field_len || line[field_len] == ' ' || line[field_len] == '\t')) {
                collecting = true;
                part = line + field_len;
                part_len = line_len - field_len;
            }
        } else if (line_len > 0 && (line[0] == ' ' || line[0] == '\t')) {
            part = line;
            part_len = line_len;
        } else {
            break;
        }
        if (part != NULL) {
            char *next = (char *)realloc(value, len + part_len + 1);
            size_t i;
            if (next == NULL) {
                free(value);
                return NULL;
            }
            value = next;
            for (i = 0; i < part_len; i++) {
                if (part[i] != '\\' && !isspace((unsigned char)part[i])) {
                    value[len++] = part[i];
                }
            }
            value[len] = '\0';
        }
        line = end == NULL ? NULL : end + 1;
    }
    return value;
}

static int scheduler_job_uid(const char *job_owner, uid_t *uid) {
    struct passwd *account;

    if (job_owner == NULL || *job_owner == '\0') {
        *uid = getuid();
        return 0;
    }
    account = getpwnam(job_owner);
    if (account == NULL) {
        return -1;
    }
    *uid = account->pw_uid;
    return 0;
}

static int set_runtime_env(FILE *job_env, const char *key, const char *value, bool export_to_job);
static int read_job_requested_resource(const char *resource_name,
                                       const char *qstat_path,
                                       char **value_out);

static int export_scheduler_slots(FILE *job_env,
                                  const char *resource_name,
                                  const char *qstat_path) {
    const char *raw;
    char *requested = NULL;
    char *end;
    double value;
    int slots;
    char slots_buf[32];

    int requested_rc = read_job_requested_resource(resource_name, qstat_path, &requested);
    if (requested_rc != LOOKUP_FOUND) {
        return requested_rc == LOOKUP_NOT_FOUND ? 0 : -1;
    }
    raw = requested;
    errno = 0;
    value = strtod(raw, &end);
    while (isspace((unsigned char)*end)) {
        end++;
    }
    if (*end == '(') {
        while (*end != '\0' && *end != ')') {
            end++;
        }
        if (*end == ')') {
            end++;
        }
        while (isspace((unsigned char)*end)) {
            end++;
        }
    }
    if (errno != 0 || *end != '\0' || value != value || value < 1 || value > INT_MAX) {
        free(requested);
        return -1;
    }
    slots = (int)value;
    if (value != (double)slots ||
        snprintf(slots_buf, sizeof(slots_buf), "%d", slots) >= (int)sizeof(slots_buf)) {
        free(requested);
        return -1;
    }
    int result = set_runtime_env(job_env, "QRMI_JOB_QPU_SLOTS", slots_buf, true);
    free(requested);
    return result;
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

static char *trim_spaces(char *text) {
    char *end;

    while (*text != '\0' && isspace((unsigned char)*text)) {
        text++;
    }
    end = text + strlen(text);
    while (end > text && isspace((unsigned char)*(end - 1))) {
        end--;
    }
    *end = '\0';
    return text;
}

static char *resource_from_list(char *list, const char *resource_name) {
    char *save = NULL;
    char *item;

    for (item = strtok_r(list, ",", &save); item != NULL; item = strtok_r(NULL, ",", &save)) {
        char *eq = strchr(item, '=');
        char *key;
        char *value;
        if (eq == NULL) {
            continue;
        }
        *eq = '\0';
        key = trim_spaces(item);
        value = trim_spaces(eq + 1);
        if (strcmp(key, resource_name) == 0 && *value != '\0') {
            return dup_text(value);
        }
    }
    return NULL;
}

/* read_job_requested_resource reads the job's hard request for resource_name
 * from qstat -j. OCS does not enforce soft requests, so a soft value could
 * name a backend the host does not offer. master_hard_resource_list holds the
 * master task's hard requests of a parallel job submitted with -scope master;
 * the prolog runs for the master task. Returns LOOKUP_*.
 */
static int read_job_requested_resource(const char *resource_name,
                                       const char *qstat_path,
                                       char **value_out) {
    static const char *const fields[] = {"hard_resource_list:", "master_hard_resource_list:"};
    const char *job_id = getenv("JOB_ID");
    char *output = NULL;
    size_t i;
    int rc;

    *value_out = NULL;
    if (job_id == NULL || *job_id == '\0') {
        return LOOKUP_HOST_ERROR;
    }
    rc = run_scheduler_command(qstat_path, "-j", job_id, &output);
    if (rc != LOOKUP_FOUND) {
        return rc;
    }
    for (i = 0; i < sizeof(fields) / sizeof(fields[0]) && *value_out == NULL; i++) {
        char *list = field_value(output, fields[i]);
        if (list != NULL) {
            *value_out = resource_from_list(list, resource_name);
            free(list);
        }
    }
    free(output);
    return *value_out == NULL ? LOOKUP_NOT_FOUND : LOOKUP_FOUND;
}

/* check_host_offers confirms that this execution host's complex_values offer
 * the requested backend, so a job can only use the backend the scheduler
 * matched it to. qconf is expected next to qstat. Returns 0, or an exit code.
 */
static int check_host_offers(const char *qstat_path, const char *resource_name, const char *backend) {
    char qconf_path[PATH_MAX];
    char host[256];
    const char *slash = strrchr(qstat_path, '/');
    char *output = NULL;
    char *list;
    char *offered;
    int rc;

    if (slash == NULL ||
        snprintf(qconf_path, sizeof(qconf_path), "%.*s/qconf", (int)(slash - qstat_path), qstat_path) >=
            (int)sizeof(qconf_path) ||
        gethostname(host, sizeof(host)) != 0) {
        return EXIT_HOST_ERROR;
    }
    host[sizeof(host) - 1] = '\0';
    rc = run_scheduler_command(qconf_path, "-se", host, &output);
    if (rc != LOOKUP_FOUND && strchr(host, '.') != NULL) {
        *strchr(host, '.') = '\0';
        rc = run_scheduler_command(qconf_path, "-se", host, &output);
    }
    if (rc != LOOKUP_FOUND) {
        log_line(stderr, "ERROR", "qconf -se %s failed", host);
        return rc == LOOKUP_RETRY ? EXIT_RESCHEDULE : EXIT_HOST_ERROR;
    }
    list = field_value(output, "complex_values");
    free(output);
    offered = list == NULL ? NULL : resource_from_list(list, resource_name);
    free(list);
    rc = offered != NULL && strcmp(offered, backend) == 0 ? 0 : EXIT_JOB_ERROR;
    if (rc != 0) {
        log_line(stderr, "ERROR", "host %s does not offer %s=%s", host, resource_name, backend);
    }
    free(offered);
    return rc;
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

/* resolve_metadata_path keeps credentials in the scheduler-owned job spool. */
static int resolve_metadata_path(char *path, size_t size) {
    const char *spool;

    spool = getenv("SGE_JOB_SPOOL_DIR");
    if (spool == NULL || *spool == '\0' ||
        snprintf(path, size, "%s/%s", spool, METADATA_FILENAME) >= (int)size) {
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

/* apply_rust_log maps the hook's --log-level value to RUST_LOG for QRMI and
 * the job. The level comes from the queue's hook command line, not from the
 * job environment.
 */
static int apply_rust_log(FILE *job_env, const char *log_level) {
    const char *level = resolve_rust_log_level(log_level);

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

int main(int argc, char **argv) {
    const char *config_path = DEFAULT_QRMI_CONFIG_PATH;
    const char *resource_name = DEFAULT_RESOURCE_NAME;
    const char *slots_resource_name = DEFAULT_SLOTS_RESOURCE_NAME;
    const char *qstat_path = NULL;
    const char *log_level = NULL;
    const char *job_owner = NULL;
    const char *granted = NULL;
    int lookup_rc;
    int exit_code = EXIT_HOST_ERROR;
    char *requested_grant = NULL;
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
    char uid_buf[32];
    uid_t job_uid;
    const char *job_id;
    const char *task_id;
    char scheduler_job_id[512];

    size_t i;
    if (has_loader_env()) {
        log_line(stderr, "ERROR", "refusing to run: LD_* or GLIBC_TUNABLES is set; start the hook through qrmi-ocs-launch");
        return EXIT_JOB_ERROR;
    }
    if (reset_process_env() != 0) {
        log_line(stderr, "ERROR", "failed to reset the environment");
        return EXIT_HOST_ERROR;
    }
    for (i = 1; i < (size_t)argc; i++) {
        if (strncmp(argv[i], "--config=", 9) == 0) {
            config_path = argv[i] + 9;
        } else if (strncmp(argv[i], "--resource=", 11) == 0) {
            resource_name = argv[i] + 11;
        } else if (strncmp(argv[i], "--slots-resource=", 17) == 0) {
            slots_resource_name = argv[i] + 17;
        } else if (strncmp(argv[i], "--qstat=", 8) == 0) {
            qstat_path = argv[i] + 8;
        } else if (strncmp(argv[i], "--log-level=", 12) == 0) {
            log_level = argv[i] + 12;
        } else if (argv[i][0] == '-' || job_owner != NULL) {
            log_line(stderr, "ERROR", "invalid prolog argument: %s", argv[i]);
            return 1;
        } else {
            job_owner = argv[i];
        }
    }
    if (*config_path == '\0' || *resource_name == '\0' ||
        qstat_path == NULL || qstat_path[0] != '/') {
        log_line(stderr, "ERROR", "config, resource, and absolute qstat path are required");
        return 1;
    }

    lookup_rc = read_job_requested_resource(resource_name, qstat_path, &requested_grant);
    if (lookup_rc == LOOKUP_NOT_FOUND) {
        log_line(stdout, "INFO", "job requests no %s resource; nothing to do", resource_name);
        return 0;
    }
    if (lookup_rc != LOOKUP_FOUND) {
        log_line(stderr, "ERROR", "failed to read scheduler state for the job");
        return lookup_rc == LOOKUP_RETRY ? EXIT_RESCHEDULE : EXIT_HOST_ERROR;
    }
    granted = requested_grant;

    if (parse_granted_backend(granted, &backend_name) != 0) {
        log_line(stderr, "ERROR", "failed to parse granted resource value: %s", granted);
        free(requested_grant);
        free(backend_name);
        return EXIT_JOB_ERROR;
    }
    free(requested_grant);
    {
        int host_rc = check_host_offers(qstat_path, resource_name, backend_name);
        if (host_rc != 0) {
            free(backend_name);
            return host_rc;
        }
    }

    if (resolve_job_env_path(job_env_path, sizeof(job_env_path)) == 0) {
        job_env_file = open_append_private(job_env_path);
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
                 "unable to resolve scheduler job environment path");
        free(backend_name);
        return 1;
    }

    /* Apply the log level before loading QRMI config so downstream operations use it. */
    if (apply_rust_log(job_env_file, log_level) != 0) {
        set_plugin_error(job_env_file, "failed to set RUST_LOG");
        goto fail;
    }
    if (scheduler_job_uid(job_owner, &job_uid) != 0 ||
        snprintf(uid_buf, sizeof(uid_buf), "%u", (unsigned int)job_uid) >= (int)sizeof(uid_buf) ||
        set_runtime_env(job_env_file, "QRMI_JOB_UID", uid_buf, true) != 0) {
        set_plugin_error(job_env_file, "failed to export scheduler job owner uid");
        goto fail;
    }
    job_id = getenv("JOB_ID");
    task_id = getenv("SGE_TASK_ID");
    if (job_id == NULL || *job_id == '\0') {
        set_plugin_error(job_env_file, "failed to export scheduler job id");
        goto fail;
    }
    if (task_id != NULL && *task_id != '\0' && strcmp(task_id, "undefined") != 0 &&
        strcmp(task_id, "0") != 0) {
        if (snprintf(scheduler_job_id, sizeof(scheduler_job_id), "%s.%s", job_id, task_id) >=
            (int)sizeof(scheduler_job_id)) {
            set_plugin_error(job_env_file, "scheduler job id is too long");
            goto fail;
        }
        job_id = scheduler_job_id;
    }
    if (set_runtime_env(job_env_file, "QRMI_JOB_ID", job_id, true) != 0) {
        set_plugin_error(job_env_file, "failed to export scheduler job id");
        goto fail;
    }
    if (*slots_resource_name != '\0' &&
        export_scheduler_slots(job_env_file, slots_resource_name, qstat_path) != 0) {
        set_plugin_error(job_env_file, "failed to export scheduler QPU slots");
        goto fail;
    }
#if defined(QRMI_OCS_HAS_LOG_CALLBACK)
    qrmi_log_callback_set(log_qrmi_line);
#endif

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
            exit_code = EXIT_JOB_ERROR;
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
            exit_code = EXIT_RESCHEDULE;
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
            exit_code = EXIT_RESCHEDULE;
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

    int metadata_fd = open(metadata_path, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW | O_CLOEXEC, 0600);
    if (metadata_fd >= 0) {
        if (fchmod(metadata_fd, 0600) != 0) {
            int chmod_errno = errno;
            close(metadata_fd);
            errno = chmod_errno;
        } else {
            metadata_file = fdopen(metadata_fd, "w");
            if (metadata_file == NULL) {
                close(metadata_fd);
            }
        }
    }
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

    return exit_code;
}
