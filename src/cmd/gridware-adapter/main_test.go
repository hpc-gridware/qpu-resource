package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	qconf "github.com/hpc-gridware/go-clusterscheduler/pkg/qconf/v9.1"
)

func TestParseCSVTrimsAndDropsEmpty(t *testing.T) {
	got := parseCSV(" ocs-master, , ocs-worker1 ,,ocs-worker2 ")
	want := []string{"ocs-master", "ocs-worker1", "ocs-worker2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCSV mismatch: got=%v want=%v", got, want)
	}
}

func TestDefaultsAreFixed(t *testing.T) {
	if defaultResourceName != "qpu" {
		t.Fatalf("defaultResourceName mismatch: got=%q want=%q", defaultResourceName, "qpu")
	}
	if defaultReportingPattern != "usage_patterns=qrmi:qrmi_*" {
		t.Fatalf("defaultReportingPattern mismatch: got=%q", defaultReportingPattern)
	}
	if defaultSlotsResourceName != "qpu_slots" {
		t.Fatalf("defaultSlotsResourceName mismatch: got=%q", defaultSlotsResourceName)
	}
	if defaultReadyResourceName != "qpu_ready" {
		t.Fatalf("defaultReadyResourceName mismatch: got=%q", defaultReadyResourceName)
	}
	if defaultSlotsScope != "host" {
		t.Fatalf("defaultSlotsScope mismatch: got=%q", defaultSlotsScope)
	}
}

func TestQPUResourceComplexEntries(t *testing.T) {
	slots := qpuSlotsComplexEntry(defaultSlotsResourceName)
	if slots.Type != qconf.ResourceTypeInt || slots.Relop != "<=" || slots.Consumable != qconf.ConsumableJOB {
		t.Fatalf("qpu_slots shape mismatch: %+v", slots)
	}
	ready := qpuReadyComplexEntry(defaultReadyResourceName)
	if ready.Type != qconf.ResourceTypeInt || ready.Relop != "<=" || ready.Consumable != qconf.ConsumableNO {
		t.Fatalf("qpu_ready shape mismatch: %+v", ready)
	}
	if ready.Default != "0" || ready.Requestable != "YES" {
		t.Fatalf("qpu_ready default/requestable mismatch: %+v", ready)
	}
}

func TestResourceOptionsValidation(t *testing.T) {
	_, err := resourceOptionsFromFlags(true, "qpu_slots", -1, "host", false, "", "", "")
	if err == nil {
		t.Fatal("expected negative slots capacity validation error")
	}
	_, err = resourceOptionsFromFlags(false, "", 1, "host", true, "qpu_ready", "", "/tmp/sensor")
	if err == nil {
		t.Fatal("expected load sensor host validation error")
	}
	_, err = resourceOptionsFromFlags(false, "", 1, "host", true, "qpu_ready", "host1", "/tmp/sensor --config /tmp/cfg")
	if err == nil {
		t.Fatal("expected load sensor path argument validation error")
	}
	_, err = resourceOptionsFromFlags(true, "qpu_slots", 1, "cluster", false, "", "", "")
	if err == nil {
		t.Fatal("expected slots scope validation error")
	}
	opts, err := resourceOptionsFromFlags(true, " qpu_slots ", 1, " GLOBAL ", true, " qpu_ready ", " host1 ", " /tmp/sensor ")
	if err != nil {
		t.Fatalf("resourceOptionsFromFlags returned error: %v", err)
	}
	if opts.slotsName != "qpu_slots" || opts.slotsScope != "global" || opts.readyName != "qpu_ready" || opts.loadSensorHost != "host1" || opts.loadSensorPath != "/tmp/sensor" {
		t.Fatalf("trimmed options mismatch: %+v", opts)
	}
}

func TestParseSingleBackendName(t *testing.T) {
	got, err := parseSingleBackendName(" test_eagle ")
	if err != nil {
		t.Fatalf("parseSingleBackendName returned error: %v", err)
	}
	if got != "test_eagle" {
		t.Fatalf("parseSingleBackendName mismatch: got=%q want=%q", got, "test_eagle")
	}

	_, err = parseSingleBackendName("EMU_FREE,test_eagle")
	if err == nil {
		t.Fatal("expected comma-separated backend validation error")
	}

	_, err = parseSingleBackendName("EMU FREE")
	if err == nil {
		t.Fatal("expected whitespace backend validation error")
	}

	_, err = parseSingleBackendName("")
	if err == nil {
		t.Fatal("expected empty backend validation error")
	}
}

func TestQRMIQueueHookCommand(t *testing.T) {
	const launcher = "/opt/qrmi/qrmi-ocs-launch"
	launcherArgs := []string{"--sge-root=/opt/ocs", "--sge-cell=default"}
	tests := []struct {
		name string
		path string
		user string
		args []string
		want string
	}{
		{name: "prolog", path: "/opt/qrmi/prolog", user: "root", args: []string{"--config=/etc/qrmi.json"}, want: "root@/opt/qrmi/qrmi-ocs-launch --sge-root=/opt/ocs --sge-cell=default /opt/qrmi/prolog --config=/etc/qrmi.json $job_owner"},
		{name: "admin user", path: "/opt/qrmi/epilog", user: "sgeadmin", want: "sgeadmin@/opt/qrmi/qrmi-ocs-launch --sge-root=/opt/ocs --sge-cell=default /opt/qrmi/epilog $job_owner"},
		{name: "disabled", path: "NONE", user: "root", want: "NONE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := qrmiQueueHookCommand(tt.path, launcher, tt.user, launcherArgs, tt.args...)
			if err != nil {
				t.Fatalf("qrmiQueueHookCommand returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("qrmiQueueHookCommand mismatch: got=%q want=%q", got, tt.want)
			}
		})
	}
	for _, bad := range []struct{ hook, launcher, user string }{
		{"/opt/qrmi/prolog --flag", launcher, "root"},
		{"qrmi@/opt/qrmi/prolog", launcher, "root"},
		{"prolog", launcher, "root"},
		{"/opt/qrmi/prolog", "qrmi-ocs-launch", "root"},
		{"/opt/qrmi/prolog", launcher, "a b"},
	} {
		if _, err := qrmiQueueHookCommand(bad.hook, bad.launcher, bad.user, nil); err == nil {
			t.Fatalf("expected %+v to be rejected", bad)
		}
	}
}

func TestOCSAdminUser(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string]string{
		"admin_user              none\nspooling_method classic\n": "root",
		"admin_user              sgeadmin\n":                      "sgeadmin",
		"spooling_method classic\n":                               "root",
	} {
		path := filepath.Join(dir, "bootstrap")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ocsAdminUser(path)
		if err != nil || got != want {
			t.Fatalf("ocsAdminUser(%q) = %q, %v; want %q", content, got, err, want)
		}
	}
	if _, err := ocsAdminUser(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected a missing bootstrap file to be an error")
	}
}

func TestSetupQRMISupportFlagValidation(t *testing.T) {
	err := runSetupQRMISupport([]string{})
	if err == nil || err.Error() != "--hosts is required" {
		t.Fatalf("expected --hosts validation error, got: %v", err)
	}

	err = runSetupQRMISupport([]string{"--hosts", "ocs-master"})
	if err == nil || err.Error() != "--host-value cannot be empty" {
		t.Fatalf("expected --host-value validation error, got: %v", err)
	}

	err = runSetupQRMISupport([]string{"--hosts", "ocs-master", "--host-value", "test_eagle"})
	if err == nil || err.Error() != "--queue is required" {
		t.Fatalf("expected --queue validation error, got: %v", err)
	}
}

func TestPrologApplyBackendEnvUsesConfiguredValue(t *testing.T) {
	prologMain, _, qrmiInclude := testSourcePaths(t)
	runCHarness(
		t,
		qrmiInclude,
		fmt.Sprintf(`#define _GNU_SOURCE
#define QRMI_OCS_TEST_TRUST_ANY_EXECUTABLE
#define main qrmi_ocs_prolog_main
#include "%s"
#undef main

struct QrmiConfig { int dummy; };
struct QrmiQuantumResource { int dummy; };

QrmiReturnCode qrmi_string_free(char *ptr) { free(ptr); return QRMI_RETURN_CODE_SUCCESS; }
QrmiConfig *qrmi_config_load(const char *filename) { (void)filename; return NULL; }
QrmiReturnCode qrmi_config_free(QrmiConfig *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }
QrmiResourceDef *qrmi_config_resource_def_get(QrmiConfig *config, const char *resource_id) {
  (void)config;
  (void)resource_id;
  return NULL;
}
const char *qrmi_config_resource_type_to_str(QrmiResourceType type) { (void)type; return "pasqal-cloud"; }
QrmiReturnCode qrmi_config_resource_def_free(QrmiResourceDef *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }
char *qrmi_get_last_error(void) { return ""; }
QrmiReturnCode qrmi_log_callback_set(QrmiLogCallback callback) { (void)callback; return QRMI_RETURN_CODE_SUCCESS; }
QrmiQuantumResource *qrmi_resource_new(const char *resource_id, QrmiResourceType resource_type) {
  (void)resource_id;
  (void)resource_type;
  static struct QrmiQuantumResource qrmi;
  return &qrmi;
}
QrmiReturnCode qrmi_resource_free(QrmiQuantumResource *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }
QrmiReturnCode qrmi_resource_is_accessible(QrmiQuantumResource *qrmi, bool *outp) {
  (void)qrmi;
  if (outp != NULL) { *outp = true; }
  return QRMI_RETURN_CODE_SUCCESS;
}
QrmiReturnCode qrmi_resource_acquire(QrmiQuantumResource *qrmi, char **acquisition_token) {
  (void)qrmi;
  if (acquisition_token != NULL) {
    char *token = (char *)malloc(4);
    if (token == NULL) { return QRMI_RETURN_CODE_ERROR; }
    memcpy(token, "tok", 4);
    *acquisition_token = token;
  }
  return QRMI_RETURN_CODE_SUCCESS;
}
QrmiReturnCode qrmi_resource_release(QrmiQuantumResource *qrmi, const char *acquisition_token) {
  (void)qrmi;
  (void)acquisition_token;
  return QRMI_RETURN_CODE_SUCCESS;
}

static int write_script(const char *path, const char *text) {
  FILE *f = fopen(path, "w");
  if (f == NULL) { return -1; }
  if (fprintf(f, "#!/bin/sh\ncat <<'EOF'\n%%sEOF\n", text) < 0) { fclose(f); return -1; }
  if (fclose(f) != 0) { return -1; }
  return chmod(path, 0700);
}

int main(void) {
  QrmiKeyValue kv = { .key = "QRMI_SAMPLE", .value = "from_config" };
  QrmiEnvironmentVariables env = { .variables = &kv, .length = 1 };
  uid_t uid;
  FILE *job_env;
  FILE *qstat;
  char qstat_template[] = "/tmp/qrmi_qstat_XXXXXX";
  char *requested = NULL;
  int qstat_fd;
  const char *value;
  if (setenv("test_backend_QRMI_SAMPLE", "override", 1) != 0) { return 90; }
  job_env = tmpfile();
  if (job_env == NULL) { return 91; }
  if (apply_backend_env(job_env, "test_backend", env) != 0) { return 1; }
  value = getenv("test_backend_QRMI_SAMPLE");
  if (value == NULL) { return 2; }
  if (strcmp(value, "from_config") != 0) { return 3; }
  if (scheduler_job_uid(NULL, &uid) != 0 || uid != getuid()) { return 4; }
  fclose(job_env);
  qstat_fd = mkstemp(qstat_template);
  if (qstat_fd < 0) { return 8; }
  qstat = fdopen(qstat_fd, "w");
  if (qstat == NULL) { close(qstat_fd); return 9; }
  if (fprintf(qstat, "#!/bin/sh\nprintf '%%%%s\\n' 'hard_resource_list: qpu=test_backend,qpu_slots=3'\n") < 0) { return 10; }
  if (fclose(qstat) != 0 || chmod(qstat_template, 0700) != 0) { return 11; }
  if (setenv("JOB_ID", "42", 1) != 0) { return 13; }
  if (read_job_requested_resource("qpu", qstat_template, &requested) != LOOKUP_FOUND || strcmp(requested, "test_backend") != 0) { return 14; }
  free(requested);
  job_env = tmpfile();
  if (job_env == NULL || export_scheduler_slots(job_env, "qpu_slots", qstat_template) != 0) { return 15; }
  fclose(job_env);
  value = getenv("QRMI_JOB_QPU_SLOTS");
  unlink(qstat_template);
  if (value == NULL || strcmp(value, "3") != 0) { return 16; }

  {
    char dir[] = "/tmp/qrmi_sched_XXXXXX";
    char qstat_path[PATH_MAX];
    char qconf_path[PATH_MAX];
    char host[256];
    char qconf_text[512];
    char *list;
    if (mkdtemp(dir) == NULL) { return 40; }
    snprintf(qstat_path, sizeof(qstat_path), "%%s/qstat", dir);
    snprintf(qconf_path, sizeof(qconf_path), "%%s/qconf", dir);
    /* Soft requests are ignored; -scope master requests count. */
    if (write_script(qstat_path, "hard_resource_list: slurm_shim=TRUE\nsoft_resource_list: qpu=EMU_FREE\n") != 0) { return 41; }
    if (read_job_requested_resource("qpu", qstat_path, &requested) != LOOKUP_NOT_FOUND) { return 42; }
    if (write_script(qstat_path, "master_hard_resource_list: h_rt=60, \\\n    qpu=PASQAL_LOCAL\n") != 0) { return 43; }
    if (read_job_requested_resource("qpu", qstat_path, &requested) != LOOKUP_FOUND || strcmp(requested, "PASQAL_LOCAL") != 0) { return 44; }
    free(requested);
    list = field_value("complex_values        qpu_slots=1, \\\n                      qpu=EMU_FREE\nload_values x\n", "complex_values");
    if (list == NULL || strcmp(list, "qpu_slots=1,qpu=EMU_FREE") != 0) { return 45; }
    free(list);
    if (gethostname(host, sizeof(host)) != 0) { return 46; }
    snprintf(qconf_text, sizeof(qconf_text), "hostname %%s\ncomplex_values        slots=20,qpu=EMU_FREE\n", host);
    if (write_script(qconf_path, qconf_text) != 0) { return 47; }
    if (check_host_offers(qstat_path, "qpu", "EMU_FREE") != 0) { return 48; }
    if (check_host_offers(qstat_path, "qpu", "PASQAL_LOCAL") != EXIT_JOB_ERROR) { return 49; }
    unlink(qstat_path);
    unlink(qconf_path);
    rmdir(dir);
  }
  /* A hook started without qrmi-ocs-launch fails closed. */
  if (has_loader_env()) { return 50; }
  if (setenv("LD_AUDIT", "/tmp/x.so", 1) != 0 || !has_loader_env()) { return 51; }
  unsetenv("LD_AUDIT");
  if (setenv("GLIBC_TUNABLES", "glibc.x=1", 1) != 0 || !has_loader_env()) { return 52; }
  unsetenv("GLIBC_TUNABLES");
  return 0;
}
`, cIncludePath(prologMain)),
	)
}

func TestEpilogStrictMetadataBehavior(t *testing.T) {
	_, epilogMain, qrmiInclude := testSourcePaths(t)
	runCHarness(
		t,
		qrmiInclude,
		fmt.Sprintf(`#define _GNU_SOURCE
#define main qrmi_ocs_epilog_main
#include "%s"
#undef main

struct QrmiQuantumResource { int dummy; };
struct QrmiConfig { int dummy; };
static int g_release_calls = 0;

QrmiConfig *qrmi_config_load(const char *filename) { (void)filename; return NULL; }
QrmiReturnCode qrmi_config_free(QrmiConfig *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }
QrmiResourceDef *qrmi_config_resource_def_get(QrmiConfig *config, const char *resource_id) {
  (void)config;
  (void)resource_id;
  return NULL;
}
QrmiReturnCode qrmi_config_resource_def_free(QrmiResourceDef *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }

char *qrmi_get_last_error(void) { return ""; }
QrmiReturnCode qrmi_log_callback_set(QrmiLogCallback callback) { (void)callback; return QRMI_RETURN_CODE_SUCCESS; }
QrmiQuantumResource *qrmi_resource_new(const char *resource_id, QrmiResourceType resource_type) {
  (void)resource_id;
  (void)resource_type;
  static struct QrmiQuantumResource qrmi;
  return &qrmi;
}
QrmiReturnCode qrmi_resource_release(QrmiQuantumResource *qrmi, const char *acquisition_token) {
  (void)qrmi;
  (void)acquisition_token;
  g_release_calls++;
  return QRMI_RETURN_CODE_SUCCESS;
}
QrmiReturnCode qrmi_resource_free(QrmiQuantumResource *ptr) { (void)ptr; return QRMI_RETURN_CODE_SUCCESS; }

int main(void) {
  AcquiredRecord rec;
  char bad_type_line[] = "res\t1x\ttok\t123\n";
  char bad_epoch_line[] = "res\t1\ttok\t123x\n";
  char good_line[] = "res\t1\ttok\t123\n";
  char spool_template[] = "/tmp/qrmi_epilog_XXXXXX";
  char metadata_path[PATH_MAX];
  char env_path[PATH_MAX];
  int metadata_fd;
  int env_fd;
  FILE *metadata;
  FILE *env_file;
  char env_buf[4096];
  size_t env_len;
  int rc;

  memset(&rec, 0, sizeof(rec));
  if (parse_record_line(bad_type_line, &rec) == 0) { return 10; }
  if (parse_record_line(bad_epoch_line, &rec) == 0) { return 11; }
  if (parse_record_line(good_line, &rec) != 0) { return 12; }
  free_record(&rec);

  if (mkdtemp(spool_template) == NULL) { return 19; }
  if (snprintf(metadata_path, sizeof(metadata_path), "%%s/%%s", spool_template, METADATA_FILENAME) >= (int)sizeof(metadata_path)) { return 20; }
  if (snprintf(env_path, sizeof(env_path), "%%s/environment", spool_template) >= (int)sizeof(env_path)) { return 20; }
  metadata_fd = open(metadata_path, O_WRONLY | O_CREAT | O_EXCL, 0600);
  if (metadata_fd < 0) { return 20; }
  metadata = fdopen(metadata_fd, "w");
  if (metadata == NULL) { return 21; }
  if (fprintf(metadata, "res\t1\ttok1\t1\nres\t1\ttok2\t2\n") < 0) { fclose(metadata); return 22; }
  fclose(metadata);

  env_fd = open(env_path, O_WRONLY | O_CREAT | O_EXCL, 0600);
  if (env_fd < 0) { return 23; }
  close(env_fd);

  if (setenv("SGE_JOB_SPOOL_DIR", spool_template, 1) != 0) { return 25; }

  {
    char *argv[] = {"qrmi-ocs-epilog", NULL};
    rc = qrmi_ocs_epilog_main(1, argv);
  }
  if (rc == 0) { return 30; }
  if (g_release_calls != 0) { return 31; }

  env_file = fopen(env_path, "r");
  if (env_file == NULL) { return 32; }
  env_len = fread(env_buf, 1, sizeof(env_buf) - 1, env_file);
  fclose(env_file);
  env_buf[env_len] = '\0';
  if (strstr(env_buf, "qrmi_release_failed=1") == NULL) { return 33; }
  if (strstr(env_buf, "qrmi_epilog_status=error") == NULL) { return 34; }

  unlink(metadata_path);
  unlink(env_path);
  rmdir(spool_template);
  return 0;
}
`, cIncludePath(epilogMain)),
	)
}

func testSourcePaths(t *testing.T) (string, string, string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	prologMain := filepath.Clean(filepath.Join(wd, "..", "qrmi-ocs-prolog", "main.c"))
	epilogMain := filepath.Clean(filepath.Join(wd, "..", "qrmi-ocs-epilog", "main.c"))
	// The harnesses compile against qrmi.h from a QRMI checkout: set
	// QRMI_INCLUDE_DIR, or check QRMI out next to this repository.
	qrmiInclude := os.Getenv("QRMI_INCLUDE_DIR")
	if qrmiInclude == "" {
		qrmiInclude = filepath.Clean(filepath.Join(wd, "..", "..", "..", "..", "qrmi"))
	}
	for _, p := range []string{prologMain, epilogMain, qrmiInclude} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("required path missing %q: %v", p, err)
		}
	}
	return prologMain, epilogMain, qrmiInclude
}

func cIncludePath(path string) string {
	return filepath.ToSlash(path)
}

func runCHarness(t *testing.T, qrmiInclude, source string) {
	t.Helper()
	gccPath, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not available")
	}

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "harness.c")
	binPath := filepath.Join(tmpDir, "harness")

	if err := os.WriteFile(srcPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write harness source failed: %v", err)
	}

	compile := exec.Command(gccPath, "-std=c11", "-Wall", "-Wextra", srcPath, "-I", qrmiInclude, "-o", binPath)
	compileOut, err := compile.CombinedOutput()
	if err != nil {
		t.Fatalf("compile harness failed: %v\n%s", err, string(compileOut))
	}

	run := exec.Command(binPath)
	runOut, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run harness failed: %v\n%s", err, string(runOut))
	}
}

// globalConfigQConf answers ShowGlobalConfiguration; other calls panic.
type globalConfigQConf struct {
	qconf.QConf
	cfg *qconf.GlobalConfig
}

func (q globalConfigQConf) ShowGlobalConfiguration() (*qconf.GlobalConfig, error) { return q.cfg, nil }

func TestLauncherSettingsComeFromTheCluster(t *testing.T) {
	t.Setenv("SGE_ROOT", "/opt/ocs/")
	t.Setenv("SGE_CELL", "")
	t.Setenv("SGE_QMASTER_PORT", "6444")
	t.Setenv("SGE_EXECD_PORT", "")
	cfg := &qconf.GlobalConfig{}
	cfg.ExecdSpoolDir = "/var/spool/ocs/execd"
	got, err := launcherSettings(globalConfigQConf{cfg: cfg})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--sge-root=/opt/ocs", "--sge-cell=default", "--spool-root=/var/spool/ocs/execd", "--qmaster-port=6444"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	t.Setenv("SGE_ROOT", "")
	if _, err := launcherSettings(globalConfigQConf{cfg: cfg}); err == nil {
		t.Fatal("expected an unset SGE_ROOT to be rejected")
	}
}

func TestWithLoadSensorReplacesEarlierInstalls(t *testing.T) {
	cases := []struct {
		current []string
		want    []string
	}{
		{nil, []string{"/opt/qrmi/bin/qrmi-ocs-load-sensor"}},
		{[]string{"NONE"}, []string{"/opt/qrmi/bin/qrmi-ocs-load-sensor"}},
		{
			[]string{"NONE", "/shared/old/qrmi-ocs-load-sensor", "/opt/site/gpu-sensor"},
			[]string{"/opt/site/gpu-sensor", "/opt/qrmi/bin/qrmi-ocs-load-sensor"},
		},
		{
			[]string{"/opt/qrmi/bin/qrmi-ocs-load-sensor"},
			[]string{"/opt/qrmi/bin/qrmi-ocs-load-sensor"},
		},
	}
	for _, tc := range cases {
		got := withLoadSensor(tc.current, "/opt/qrmi/bin/qrmi-ocs-load-sensor")
		if !slices.Equal(got, tc.want) {
			t.Errorf("withLoadSensor(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
}
