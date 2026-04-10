package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
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
const char *qrmi_get_last_error(void) { return ""; }
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

int main(void) {
  QrmiKeyValue kv = { .key = "QRMI_SAMPLE", .value = "from_config" };
  QrmiEnvironmentVariables env = { .variables = &kv, .length = 1 };
  FILE *job_env;
  const char *value;
  if (setenv("test_backend_QRMI_SAMPLE", "override", 1) != 0) { return 90; }
  job_env = tmpfile();
  if (job_env == NULL) { return 91; }
  if (apply_backend_env(job_env, "test_backend", env) != 0) { return 1; }
  value = getenv("test_backend_QRMI_SAMPLE");
  fclose(job_env);
  if (value == NULL) { return 2; }
  if (strcmp(value, "from_config") != 0) { return 3; }
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
static int g_release_calls = 0;

const char *qrmi_get_last_error(void) { return ""; }
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
  char metadata_template[] = "/tmp/qrmi_epilog_meta_XXXXXX";
  char env_template[] = "/tmp/qrmi_epilog_env_XXXXXX";
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

  metadata_fd = mkstemp(metadata_template);
  if (metadata_fd < 0) { return 20; }
  metadata = fdopen(metadata_fd, "w");
  if (metadata == NULL) { return 21; }
  if (fprintf(metadata, "res\t1\ttok1\t1\nres\t1\ttok2\t2\n") < 0) { fclose(metadata); return 22; }
  fclose(metadata);

  env_fd = mkstemp(env_template);
  if (env_fd < 0) { return 23; }
  close(env_fd);

  if (setenv("QRMI_OCS_METADATA_PATH", metadata_template, 1) != 0) { return 24; }
  if (setenv("SGE_JOB_ENV", env_template, 1) != 0) { return 25; }

  rc = qrmi_ocs_epilog_main();
  if (rc == 0) { return 30; }
  if (g_release_calls != 0) { return 31; }

  env_file = fopen(env_template, "r");
  if (env_file == NULL) { return 32; }
  env_len = fread(env_buf, 1, sizeof(env_buf) - 1, env_file);
  fclose(env_file);
  env_buf[env_len] = '\0';
  if (strstr(env_buf, "qrmi_release_failed=1") == NULL) { return 33; }
  if (strstr(env_buf, "qrmi_epilog_status=error") == NULL) { return 34; }

  unlink(env_template);
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
	qrmiInclude := filepath.Clean(filepath.Join(wd, "..", "..", "..", "..", "qrmi"))
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
