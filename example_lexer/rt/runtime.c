#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <math.h>
#include <errno.h>
#include <unistd.h>
typedef struct { const char *p; int64_t n; } kstr;

// Required competition binary signature (Rule 1: survives -O2 and stripping)
const char kopaj_flag[] __attribute__((used)) = "sign kopaj";

int _kpj_argc = 0;
char **_kpj_argv = NULL;
static int _kpj_nread = 0;

static char _kpj_out[1 << 16];
static size_t _kpj_outn = 0;

void _kpj_flush(void) {
    size_t off = 0;
    while (off < _kpj_outn) {
        ssize_t w = write(1, _kpj_out + off, _kpj_outn - off);
        if (w < 0) { if (errno == EINTR) continue; break; }
        if (w == 0) break;
        off += (size_t)w;
    }
    _kpj_outn = 0;
}

void _kpj_put(const char *s, size_t n) {
    if (n > sizeof(_kpj_out) - _kpj_outn) {
        _kpj_flush();
        if (n > sizeof(_kpj_out)) {
            size_t off = 0;
            while (off < n) {
                ssize_t w = write(1, s + off, n - off);
                if (w < 0) { if (errno == EINTR) continue; break; }
                if (w == 0) break;
                off += (size_t)w;
            }
            return;
        }
    }
    memcpy(_kpj_out + _kpj_outn, s, n);
    _kpj_outn += n;
}

static char *_kpj_in = NULL;
static size_t _kpj_inlen = 0, _kpj_inpos = 0;
static int _kpj_loaded = 0;

void _kpj_load(void) {
    _kpj_loaded = 1;
    size_t cap = 1 << 16;
    _kpj_in = (char *)malloc(cap + 1);
    if (!_kpj_in) { _kpj_in = (char *)""; return; }
    if (isatty(0)) { _kpj_in[0] = 0; return; }
    for (;;) {
        if (_kpj_inlen == cap) {
            cap <<= 1;
            char *n = (char *)realloc(_kpj_in, cap + 1);
            if (!n) break;
            _kpj_in = n;
        }
        ssize_t r = read(0, _kpj_in + _kpj_inlen, cap - _kpj_inlen);
        if (r < 0) { if (errno == EINTR) continue; break; }
        if (r == 0) break;
        _kpj_inlen += (size_t)r;
    }
    _kpj_in[_kpj_inlen] = 0;
}

static size_t _kpj_toklen = 0;

const char *_kpj_tok(void) {
    if (!_kpj_loaded) _kpj_load();
    while (_kpj_inpos < _kpj_inlen && (unsigned char)_kpj_in[_kpj_inpos] <= ' ') _kpj_inpos++;
    if (_kpj_inpos >= _kpj_inlen) return NULL;
    const char *s = _kpj_in + _kpj_inpos;
    while (_kpj_inpos < _kpj_inlen && (unsigned char)_kpj_in[_kpj_inpos] > ' ') _kpj_inpos++;
    _kpj_toklen = (size_t)(_kpj_in + _kpj_inpos - s);
    return s;
}

const char *_kpj_next(void) {
    _kpj_nread++;
    const char *s = _kpj_tok();
    if (!s && _kpj_nread < _kpj_argc) { s = _kpj_argv[_kpj_nread]; _kpj_toklen = strlen(s); }
    return s;
}

int64_t _kpj_parse_int(const char *s) {
    if (!s) return 0;
    int neg = 0;
    if (*s == '-') { neg = 1; s++; } else if (*s == '+') s++;
    uint64_t u = 0;
    while (*s >= '0' && *s <= '9') { u = u * 10 + (uint64_t)(*s - '0'); s++; }
    return (int64_t)(neg ? (0 - u) : u);
}




kstr _kpj_mk(const char *p, int64_t n) { kstr s; s.p = p; s.n = n; return s; }

kstr _kpj_read_token(void) {
    const char *s = _kpj_next();
    if (!s) return _kpj_mk("", 0);
    return _kpj_mk(s, (int64_t)_kpj_toklen);
}

kstr _kpj_read_line(void) {
    if (!_kpj_loaded) _kpj_load();
    while (_kpj_inpos < _kpj_inlen && (_kpj_in[_kpj_inpos] == ' ' || _kpj_in[_kpj_inpos] == '\t')) _kpj_inpos++;
    size_t st = _kpj_inpos;
    while (_kpj_inpos < _kpj_inlen && _kpj_in[_kpj_inpos] != '\n') _kpj_inpos++;
    size_t en = _kpj_inpos;
    if (_kpj_inpos < _kpj_inlen) _kpj_inpos++;
    while (en > st && (_kpj_in[en - 1] == '\r' || _kpj_in[en - 1] == ' ' || _kpj_in[en - 1] == '\t')) en--;
    return _kpj_mk(_kpj_in + st, (int64_t)(en - st));
}

int64_t _kpj_len(kstr s) { return s.n; }

kstr _kpj_upper(kstr s) {
    char *b = (char *)malloc((size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (c >= 'a' && c <= 'z') ? (char)(c - 32) : c; }
    b[s.n] = 0;
    return _kpj_mk(b, s.n);
}

kstr _kpj_lower(kstr s) {
    char *b = (char *)malloc((size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (c >= 'A' && c <= 'Z') ? (char)(c + 32) : c; }
    b[s.n] = 0;
    return _kpj_mk(b, s.n);
}

kstr _kpj_char_at(kstr s, int64_t i) {
    if (i < 0 || i >= s.n) return _kpj_mk("", 0);
    return _kpj_mk(s.p + i, 1);
}

kstr _kpj_substr(kstr s, int64_t st, int64_t len) {
    if (st < 0) st = 0;
    if (st > s.n) st = s.n;
    if (len < 0) len = 0;
    if (len > s.n - st) len = s.n - st;
    return _kpj_mk(s.p + st, len);
}

kstr _kpj_cat(kstr a, kstr b) {
    char *r = (char *)malloc((size_t)(a.n + b.n) + 1);
    memcpy(r, a.p, (size_t)a.n);
    memcpy(r + a.n, b.p, (size_t)b.n);
    r[a.n + b.n] = 0;
    return _kpj_mk(r, a.n + b.n);
}

int _kpj_scmp(kstr a, kstr b) {
    int64_t m = a.n < b.n ? a.n : b.n;
    int c = m ? memcmp(a.p, b.p, (size_t)m) : 0;
    if (c) return c;
    return a.n < b.n ? -1 : (a.n > b.n ? 1 : 0);
}

int64_t _kpj_seq(kstr a, kstr b) {
    return a.n == b.n && (a.n == 0 || memcmp(a.p, b.p, (size_t)a.n) == 0);
}

kstr _kpj_str_int(int64_t v) {
    char *b = (char *)malloc(24);
    int n = snprintf(b, 24, "%lld", (long long)v);
    return _kpj_mk(b, n);
}

kstr _kpj_str_float(double v) {
    char *b = (char *)malloc(40);
    int n = snprintf(b, 40, "%.17g", v);
    return _kpj_mk(b, n);
}

void _kpj_w_str(kstr s) { _kpj_put(s.p, (size_t)s.n); }

int64_t _kpj_prime_pi(int64_t n) {
    if (n < 2 || n > 400000000) return 0;
    size_t sz = (size_t)n + 1;
    unsigned char *c = (unsigned char *)calloc(sz, 1);
    if (!c) return 0;
    int64_t cnt = 0;
    for (int64_t i = 2; i <= n; i++) {
        if (!c[i]) {
            cnt++;
            for (int64_t j = i * i; j <= n; j += i) c[j] = 1;
        }
    }
    free(c);
    return cnt;
}

int64_t _kpj_read_int(void) { return _kpj_parse_int(_kpj_next()); }

double _kpj_read_float(void) {
    const char *s = _kpj_next();
    if (!s) return 0.0;
    return strtod(s, NULL);
}

int64_t _kpj_arg_int(int64_t idx) {
    if (idx >= 0 && idx < (int64_t)_kpj_argc) return _kpj_parse_int(_kpj_argv[idx]);
    return 0;
}

int64_t _kpj_argc_fn(void) { return (int64_t)(_kpj_argc > 0 ? _kpj_argc - 1 : 0); }

void _kpj_w_int(int64_t v) {
    char b[24];
    char *e = b + sizeof(b);
    uint64_t u = v < 0 ? (0 - (uint64_t)v) : (uint64_t)v;
    do { *--e = (char)('0' + u % 10); u /= 10; } while (u);
    if (v < 0) *--e = '-';
    _kpj_put(e, (size_t)(b + sizeof(b) - e));
}

void _kpj_w_float(double v) {
    if (v == 0) v = 0.0;
    char b[64];
    int n = 0;
    for (int prec = 15; prec <= 17; prec++) {
        n = snprintf(b, sizeof(b) - 1, "%.*g", prec, v);
        if (prec == 17 || strtod(b, NULL) == v) break;
    }
    _kpj_put(b, (size_t)n);
}

int64_t _kpj_div(int64_t a, int64_t b) {
    if (b == 0) return 0;
    if (b == -1) return (int64_t)(0 - (uint64_t)a);
    return a / b;
}

int64_t _kpj_mod(int64_t a, int64_t b) {
    if (b == 0 || b == -1) return 0;
    return a % b;
}


void kpj_put_space(void) { _kpj_put(" ", 1); }
void kpj_put_nl(void) { _kpj_put("\n", 1); }
void kpj_exit(int c) { exit(c); }
int64_t kpj_isfinite(double x) { return (int64_t)isfinite(x); }
double kpj_log(double x) { return log(x); }
double kpj_exp(double x) { return exp(x); }
double kpj_sin(double x) { return sin(x); }
double kpj_cos(double x) { return cos(x); }
double kpj_tan(double x) { return tan(x); }
double kpj_floor(double x) { return floor(x); }
double kpj_ceil(double x) { return ceil(x); }
double kpj_pow(double x, double y) { return pow(x, y); }
double kpj_fmod(double x, double y) { return fmod(x, y); }

/* Reserved area: the compiler patches generated machine code + literals here. */
__asm__(".section .kpjcode,\"ax\",@progbits\n"
        ".balign 4096\n"
        ".globl kpj_code\n"
        "kpj_code:\n"
        ".skip 262144\n"
        ".text\n");
extern char kpj_code[];

int main(int argc, char **argv) {
    _kpj_argc = argc;
    _kpj_argv = argv;
    atexit(_kpj_flush);
    if (kopaj_flag[0] == '\0') return 1;
    return ((int (*)(void))kpj_code)();
}
