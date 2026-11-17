pub const C_RUNTIME: &str = r##"#include <stdint.h>
#include <stddef.h>

typedef long kssize;

// Required competition binary signature (Rule 1: survives -O3, --gc-sections, and stripping)
__attribute__((used, section(".rodata"))) static const char kopaj_flag[] = "sign kopaj";

/* ---------------- syscalls ---------------- */
static __attribute__((always_inline)) inline long _kpj_sys3(long n, long a, long b, long c) {
    long r;
    __asm__ volatile ("syscall" : "=a"(r) : "a"(n), "D"(a), "S"(b), "d"(c) : "rcx", "r11", "memory");
    return r;
}
static __attribute__((always_inline)) inline long _kpj_sys6(long n, long a, long b, long c, long d, long e, long f) {
    long r;
    register long r10 __asm__("r10") = d;
    register long r8 __asm__("r8") = e;
    register long r9 __asm__("r9") = f;
    __asm__ volatile ("syscall" : "=a"(r) : "a"(n), "D"(a), "S"(b), "d"(c), "r"(r10), "r"(r8), "r"(r9) : "rcx", "r11", "memory");
    return r;
}

#ifdef KPJ_FREESTANDING
void *memcpy(void *d, const void *s, size_t n) {
    void *r = d;
    __asm__ volatile ("rep movsb" : "+D"(d), "+S"(s), "+c"(n) : : "memory");
    return r;
}
void *memmove(void *d, const void *s, size_t n) {
    unsigned char *dd = (unsigned char *)d; const unsigned char *ss = (const unsigned char *)s;
    if (dd <= ss || dd >= ss + n) return memcpy(d, s, n);
    while (n) { n--; dd[n] = ss[n]; }
    return d;
}
void *memset(void *d, int c, size_t n) {
    void *r = d;
    __asm__ volatile ("rep stosb" : "+D"(d), "+c"(n) : "a"(c) : "memory");
    return r;
}
int memcmp(const void *a, const void *b, size_t n) {
    const unsigned char *x = (const unsigned char *)a, *y = (const unsigned char *)b;
    for (size_t i = 0; i < n; i++) if (x[i] != y[i]) return x[i] < y[i] ? -1 : 1;
    return 0;
}
size_t strlen(const char *s) { const char *p = s; while (*p) p++; return (size_t)(p - s); }
#else
#include <string.h>
#endif

typedef struct { const char *p; int64_t n; } kstr;

/* All mutable runtime state lives on the initial stack (zero .data, zero .bss, zero extra VMAs) */
typedef struct {
    int argc;
    int nread;
    char **argv;
    size_t outn;
    size_t inlen;
    size_t inpos;
    int loaded;
    size_t hoff;
    char out[1024];
    char in[1024];
    char heap[1024] __attribute__((aligned(16)));
} KpjCtx;

static inline void _kpj_rawwrite(const char *p, size_t n) {
    while (n) {
        long w = _kpj_sys3(1, 1, (long)p, (long)n);
        if (w == -4) continue;
        if (w <= 0) break;
        p += w; n -= (size_t)w;
    }
}
static inline void _kpj_flush(KpjCtx *ctx) {
    if (ctx->outn) { _kpj_rawwrite(ctx->out, ctx->outn); ctx->outn = 0; }
}

static __attribute__((always_inline)) inline void _kpj_put(KpjCtx *ctx, const char *s, size_t n) {
    if (__builtin_expect(n > sizeof(ctx->out) - ctx->outn, 0)) {
        _kpj_flush(ctx);
        if (n > sizeof(ctx->out)) { _kpj_rawwrite(s, n); return; }
    }
    for (size_t i = 0; i < n; i++) ctx->out[ctx->outn + i] = s[i];
    ctx->outn += n;
}
static __attribute__((always_inline)) inline void _kpj_putc(KpjCtx *ctx, char c) {
    if (__builtin_expect(ctx->outn == sizeof(ctx->out), 0)) _kpj_flush(ctx);
    ctx->out[ctx->outn++] = c;
}

__attribute__((noreturn, always_inline)) static inline void _kpj_exit(KpjCtx *ctx, int code) {
    _kpj_flush(ctx);
    for (;;) _kpj_sys3(231, code, 0, 0);
}

static inline void *_kpj_alloc(KpjCtx *ctx, size_t n) {
    n = (n + 15) & ~(size_t)15;
    if (__builtin_expect(ctx->hoff + n <= sizeof(ctx->heap), 1)) {
        void *p = ctx->heap + ctx->hoff;
        ctx->hoff += n;
        return p;
    }
    size_t c = (n + 4095) & ~(size_t)4095;
    long r = _kpj_sys6(9, 0, (long)c, 3, 0x22, -1, 0);
    if (r < 0 && r > -4096) { _kpj_put(ctx, "error\n", 6); _kpj_exit(ctx, 1); }
    return (void *)r;
}

/* ---------------- input: zero-syscall argv fast-path + single-read stdin ---------------- */
static inline void _kpj_load_stdin(KpjCtx *ctx) {
    ctx->loaded = 1;
    for (;;) {
        long r = _kpj_sys3(0, 0, (long)ctx->in, (long)(sizeof(ctx->in) - 1));
        if (r == -4) continue;
        if (r > 0) ctx->inlen = (size_t)r;
        break;
    }
    ctx->in[ctx->inlen] = 0;
}

static inline kstr _kpj_tok(KpjCtx *ctx) {
    if (!ctx->loaded) _kpj_load_stdin(ctx);
    while (ctx->inpos < ctx->inlen && (unsigned char)ctx->in[ctx->inpos] <= ' ') ctx->inpos++;
    if (ctx->inpos >= ctx->inlen) { kstr z = {"", 0}; return z; }
    size_t st = ctx->inpos;
    while (ctx->inpos < ctx->inlen && (unsigned char)ctx->in[ctx->inpos] > ' ') ctx->inpos++;
    kstr res = {ctx->in + st, (int64_t)(ctx->inpos - st)};
    return res;
}

/* Fast raw pointer for numeric parsers: no length scan needed on argv! */
static __attribute__((always_inline)) inline const char *_kpj_next_raw(KpjCtx *ctx) {
    int idx = ++ctx->nread;
    if (__builtin_expect(ctx->argc > 1, 1)) {
        return __builtin_expect(idx < ctx->argc, 1) ? ctx->argv[idx] : 0;
    }
    kstr t = _kpj_tok(ctx);
    return t.n ? t.p : 0;
}

static __attribute__((always_inline)) inline kstr _kpj_read_token(KpjCtx *ctx) {
    int idx = ++ctx->nread;
    if (__builtin_expect(ctx->argc > 1, 1)) {
        if (__builtin_expect(idx < ctx->argc, 1)) {
            const char *s = ctx->argv[idx];
            while (*s && (unsigned char)*s <= ' ') s++;
            const char *e = s;
            while (*e && (unsigned char)*e > ' ') e++;
            kstr res = {s, (int64_t)(e - s)};
            return res;
        }
        kstr z = {"", 0};
        return z;
    }
    return _kpj_tok(ctx);
}

/* ---------------- numbers ---------------- */
static __attribute__((always_inline)) inline int64_t _kpj_parse_int(const char *s) {
    if (!s) return 0;
    while (*s && (unsigned char)*s <= ' ') s++;
    int neg = 0;
    if (*s == '-') { neg = 1; s++; } else if (*s == '+') s++;
    uint64_t u = 0;
    while ((unsigned)(*s - '0') < 10u) { u = u * 10 + (uint64_t)(*s - '0'); s++; }
    return (int64_t)(neg ? (0 - u) : u);
}

static inline long double _kpj_p10l(int n) {
    long double r = 1.0L, b = 10.0L;
    while (n) { if (n & 1) r *= b; b *= b; n >>= 1; }
    return r;
}

static inline double _kpj_parse_float(const char *s) {
    if (!s) return 0.0;
    while (*s && (unsigned char)*s <= ' ') s++;
    int neg = 0;
    if (*s == '-') { neg = 1; s++; } else if (*s == '+') s++;
    if ((*s | 32) == 'i') return neg ? -__builtin_inf() : __builtin_inf();
    if ((*s | 32) == 'n') return __builtin_nan("");
    uint64_t m = 0; int nd = 0, e10 = 0;
    while ((unsigned)(*s - '0') < 10u) {
        if (nd < 19) { m = m * 10 + (uint64_t)(*s - '0'); if (m) nd++; } else e10++;
        s++;
    }
    if (*s == '.') {
        s++;
        while ((unsigned)(*s - '0') < 10u) {
            if (nd < 19) { m = m * 10 + (uint64_t)(*s - '0'); if (m) nd++; e10--; }
            s++;
        }
    }
    if ((*s | 32) == 'e') {
        s++;
        int en = 0, ex = 0;
        if (*s == '-') { en = 1; s++; } else if (*s == '+') s++;
        while ((unsigned)(*s - '0') < 10u) { if (ex < 100000) ex = ex * 10 + (*s - '0'); s++; }
        e10 += en ? -ex : ex;
    }
    long double r = (long double)m;
    if (m && e10) {
        if (e10 > 5000) r = __builtin_infl();
        else if (e10 < -5000) r = 0.0L;
        else if (e10 > 0) r *= _kpj_p10l(e10);
        else r /= _kpj_p10l(-e10);
    }
    double d = (double)r;
    return neg ? -d : d;
}

static __attribute__((always_inline)) inline int64_t _kpj_read_int(KpjCtx *ctx) { return _kpj_parse_int(_kpj_next_raw(ctx)); }
static inline double _kpj_read_float(KpjCtx *ctx) { return _kpj_parse_float(_kpj_next_raw(ctx)); }

static inline int64_t _kpj_arg_int(KpjCtx *ctx, int64_t idx) {
    if (idx >= 0 && idx < (int64_t)ctx->argc) return _kpj_parse_int(ctx->argv[idx]);
    return 0;
}
static inline int64_t _kpj_argc_fn(KpjCtx *ctx) { return (int64_t)(ctx->argc > 0 ? ctx->argc - 1 : 0); }

static inline int _kpj_fmt_int(char *out, int64_t v) {
    char b[24]; char *e = b + sizeof(b), *p = e;
    uint64_t u = v < 0 ? (0 - (uint64_t)v) : (uint64_t)v;
    do { *--p = (char)('0' + u % 10); u /= 10; } while (u);
    if (v < 0) *--p = '-';
    int n = (int)(e - p);
    for (int i = 0; i < n; i++) out[i] = p[i];
    return n;
}

static __attribute__((always_inline)) inline void _kpj_w_int(KpjCtx *ctx, int64_t v) {
    char b[24]; char *e = b + sizeof(b), *p = e;
    uint64_t u = v < 0 ? (0 - (uint64_t)v) : (uint64_t)v;
    do { *--p = (char)('0' + u % 10); u /= 10; } while (u);
    if (v < 0) *--p = '-';
    _kpj_put(ctx, p, (size_t)(e - p));
}

static inline int _kpj_fmt_float(char *o, double v) {
    int n = 0;
    if (v != v) { o[0] = 'n'; o[1] = 'a'; o[2] = 'n'; return 3; }
    if (v < 0) { o[n++] = '-'; v = -v; }
    if (v == __builtin_inf()) { o[n] = 'i'; o[n+1] = 'n'; o[n+2] = 'f'; return n + 3; }
    if (v == 0) { o[0] = '0'; return 1; }
    if (v < 1e16 && v == (double)(uint64_t)v) {
        return n + _kpj_fmt_int(o + n, (int64_t)(uint64_t)v);
    }
    union { double d; uint64_t u; } bits; bits.d = v;
    int bexp = (int)((bits.u >> 52) & 0x7ff) - 1023;
    int e = (int)(bexp * 0.30102999566398120);
    long double y = (long double)v;
    if (15 - e >= 0) y *= _kpj_p10l(15 - e); else y /= _kpj_p10l(e - 15);
    while (y >= 1e16L) { y /= 10.0L; e++; }
    while (y < 1e15L) { y *= 10.0L; e--; }
    uint64_t m = (uint64_t)(y + 0.5L);
    if (m >= 10000000000000000ULL) { m /= 10; e++; }
    char d[16];
    for (int i = 15; i >= 0; i--) { d[i] = (char)('0' + m % 10); m /= 10; }
    int nd = 16;
    while (nd > 1 && d[nd - 1] == '0') nd--;
    if (e >= 0 && e < 16) {
        for (int i = 0; i <= e; i++) o[n++] = i < nd ? d[i] : '0';
        if (nd > e + 1) { o[n++] = '.'; for (int i = e + 1; i < nd; i++) o[n++] = d[i]; }
    } else if (e < 0 && e >= -5) {
        o[n++] = '0'; o[n++] = '.';
        for (int i = 0; i < -e - 1; i++) o[n++] = '0';
        for (int i = 0; i < nd; i++) o[n++] = d[i];
    } else {
        o[n++] = d[0];
        if (nd > 1) { o[n++] = '.'; for (int i = 1; i < nd; i++) o[n++] = d[i]; }
        o[n++] = 'e';
        n += _kpj_fmt_int(o + n, e);
    }
    return n;
}

static inline void _kpj_w_float(KpjCtx *ctx, double v) {
    char b[48];
    int n = _kpj_fmt_float(b, v);
    _kpj_put(ctx, b, (size_t)n);
}

static __attribute__((always_inline)) inline int64_t _kpj_div(int64_t a, int64_t b) {
    if (__builtin_expect(b == 0, 0)) return 0;
    if (__builtin_expect(b == -1, 0)) return (int64_t)(0 - (uint64_t)a);
    return a / b;
}
static __attribute__((always_inline)) inline int64_t _kpj_mod(int64_t a, int64_t b) {
    if (__builtin_expect(b == 0 || b == -1, 0)) return 0;
    return a % b;
}

/* ---------------- math (SSE2 + x87, no libm) ---------------- */
static inline double _kpj_sqrt(double x) {
    double r;
    __asm__ ("sqrtsd %1, %0" : "=x"(r) : "x"(x));
    return r;
}
static inline double _kpj_fabs(double x) { return __builtin_fabs(x); }

static inline long double _kpj_log2l(long double x) {
    long double r;
    __asm__ ("fld1; fxch; fyl2x" : "=t"(r) : "0"(x));
    return r;
}
static inline long double _kpj_exp2l(long double y) {
    long double i, f, r, o;
    __asm__ ("frndint" : "=t"(i) : "0"(y));
    f = y - i;
    __asm__ ("f2xm1" : "=t"(r) : "0"(f));
    r += 1.0L;
    __asm__ ("fscale" : "=t"(o) : "0"(r), "u"(i));
    return o;
}
static inline double _kpj_log(double x) {
    if (x != x || x < 0) return __builtin_nan("");
    if (x == 0) return -__builtin_inf();
    long double r;
    __asm__ ("fldln2; fxch; fyl2x" : "=t"(r) : "0"((long double)x));
    return (double)r;
}
static inline double _kpj_exp(double x) {
    if (x != x) return x;
    if (x > 1000) return __builtin_inf();
    if (x < -1000) return 0.0;
    return (double)_kpj_exp2l((long double)x * 1.44269504088896340735992468100189214L);
}
static inline double _kpj_floor(double x) {
    if (!(__builtin_fabs(x) < 4503599627370496.0)) return x;
    double t = (double)(int64_t)x;
    return t > x ? t - 1.0 : t;
}
static inline double _kpj_ceil(double x) {
    if (!(__builtin_fabs(x) < 4503599627370496.0)) return x;
    double t = (double)(int64_t)x;
    return t < x ? t + 1.0 : t;
}
static inline double _kpj_pow(double b, double e) {
    if (e == 0) return 1.0;
    if (b != b || e != e) return __builtin_nan("");
    int eint = (_kpj_floor(e) == e);
    if (eint && __builtin_fabs(e) <= 1024) {
        long double r = 1.0L, x = b;
        int64_t n = (int64_t)e; int neg = n < 0; if (neg) n = -n;
        while (n) { if (n & 1) r *= x; x *= x; n >>= 1; }
        if (neg) r = 1.0L / r;
        return (double)r;
    }
    if (b == 0) return e > 0 ? 0.0 : __builtin_inf();
    if (b < 0 && !eint) return __builtin_nan("");
    long double l = (long double)e * _kpj_log2l(b < 0 ? -(long double)b : (long double)b);
    if (l > 20000) return (b < 0 && _kpj_floor(e / 2) * 2 != e) ? -__builtin_inf() : __builtin_inf();
    if (l < -20000) return 0.0;
    double r = (double)_kpj_exp2l(l);
    if (b < 0 && _kpj_floor(e / 2) * 2 != e) r = -r;
    return r;
}
static inline long double _kpj_reduce(long double x) {
    if (__builtin_fabsl(x) < 9.2e18L) return x;
    long double twopi = 6.28318530717958647692528676655900577L, r;
    __asm__ ("1: fprem1; fnstsw %%ax; testb $4, %%ah; jnz 1b" : "=t"(r) : "0"(x), "u"(twopi) : "ax", "cc");
    return r;
}
static inline double _kpj_sin(double x) {
    if (!__builtin_isfinite(x)) return __builtin_nan("");
    long double r; __asm__ ("fsin" : "=t"(r) : "0"(_kpj_reduce(x))); return (double)r;
}
static inline double _kpj_cos(double x) {
    if (!__builtin_isfinite(x)) return __builtin_nan("");
    long double r; __asm__ ("fcos" : "=t"(r) : "0"(_kpj_reduce(x))); return (double)r;
}
static inline double _kpj_tan(double x) {
    if (!__builtin_isfinite(x)) return __builtin_nan("");
    long double r; __asm__ ("fptan; fstp %%st(0)" : "=t"(r) : "0"(_kpj_reduce(x))); return (double)r;
}
static inline double _kpj_fmod(double a, double b) {
    if (b == 0 || a != a || b != b || !__builtin_isfinite(a)) return __builtin_nan("");
    long double r;
    __asm__ ("1: fprem; fnstsw %%ax; testb $4, %%ah; jnz 1b" : "=t"(r) : "0"((long double)a), "u"((long double)b) : "ax", "cc");
    return (double)r;
}

/* L1-cache bitset odd-only sieve of Eratosthenes on stack (zero mmap for N <= 200000) */
static inline int64_t _kpj_prime_pi(KpjCtx *ctx, int64_t n) {
    if (n < 2) return 0;
    int64_t half = (n - 1) >> 1;
    uint64_t stack_bits[1600];
    uint64_t *bits = stack_bits;
    size_t nwords = ((size_t)half >> 6) + 1;
    if (__builtin_expect(nwords <= 1600, 1)) {
        for (size_t i = 0; i < nwords; i++) bits[i] = 0;
    } else {
        bits = (uint64_t *)_kpj_alloc(ctx, nwords * sizeof(uint64_t));
    }
    bits[0] = 1ULL;
    for (int64_t i = 1; ; i++) {
        int64_t p = (i << 1) + 1;
        int64_t j = (p * p - 1) >> 1;
        if (j > half) break;
        if (!(bits[i >> 6] & (1ULL << (i & 63)))) {
            for (; j <= half; j += p) bits[j >> 6] |= (1ULL << (j & 63));
        }
    }
    int64_t cnt = 1;
    size_t full = (size_t)half >> 6;
    for (size_t w = 0; w < full; w++) {
        cnt += 64 - __builtin_popcountll(bits[w]);
    }
    int rem = (int)(half & 63);
    uint64_t mask = (rem == 63) ? ~0ULL : ((1ULL << (rem + 1)) - 1ULL);
    cnt += (rem + 1) - __builtin_popcountll(bits[full] & mask);
    return cnt;
}

/* ---------------- strings ---------------- */
static __attribute__((always_inline)) inline kstr _kpj_mk(const char *p, int64_t n) { kstr s = {p, n}; return s; }

static inline kstr _kpj_read_line(KpjCtx *ctx) {
    if (!ctx->loaded) _kpj_load_stdin(ctx);
    while (ctx->inpos < ctx->inlen && (ctx->in[ctx->inpos] == ' ' || ctx->in[ctx->inpos] == '\t')) ctx->inpos++;
    if (ctx->inpos >= ctx->inlen) return _kpj_mk("", 0);
    size_t st = ctx->inpos;
    while (ctx->inpos < ctx->inlen && ctx->in[ctx->inpos] != '\n') ctx->inpos++;
    size_t en = ctx->inpos;
    if (ctx->inpos < ctx->inlen) ctx->inpos++;
    while (en > st && (ctx->in[en - 1] == '\r' || ctx->in[en - 1] == ' ' || ctx->in[en - 1] == '\t')) en--;
    return _kpj_mk(ctx->in + st, (int64_t)(en - st));
}

static __attribute__((always_inline)) inline int64_t _kpj_len(kstr s) { return s.n; }

static inline kstr _kpj_upper(KpjCtx *ctx, kstr s) {
    char *b = (char *)_kpj_alloc(ctx, (size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (char)((unsigned)(c - 'a') < 26u ? c - 32 : c); }
    return _kpj_mk(b, s.n);
}
static inline kstr _kpj_lower(KpjCtx *ctx, kstr s) {
    char *b = (char *)_kpj_alloc(ctx, (size_t)s.n + 1);
    for (int64_t i = 0; i < s.n; i++) { char c = s.p[i]; b[i] = (char)((unsigned)(c - 'A') < 26u ? c + 32 : c); }
    return _kpj_mk(b, s.n);
}
static __attribute__((always_inline)) inline kstr _kpj_char_at(kstr s, int64_t i) {
    if (i < 0 || i >= s.n) return _kpj_mk("", 0);
    return _kpj_mk(s.p + i, 1);
}
static __attribute__((always_inline)) inline kstr _kpj_substr(kstr s, int64_t st, int64_t len) {
    if (st < 0) st = 0;
    if (st > s.n) st = s.n;
    if (len < 0) len = 0;
    if (len > s.n - st) len = s.n - st;
    return _kpj_mk(s.p + st, len);
}
static inline kstr _kpj_cat(KpjCtx *ctx, kstr a, kstr b) {
    if (!b.n) return a;
    if (!a.n) return b;
    if (a.p + a.n == b.p) return _kpj_mk(a.p, a.n + b.n);
    char *r = (char *)_kpj_alloc(ctx, (size_t)(a.n + b.n) + 1);
    for (int64_t i = 0; i < a.n; i++) r[i] = a.p[i];
    for (int64_t i = 0; i < b.n; i++) r[a.n + i] = b.p[i];
    return _kpj_mk(r, a.n + b.n);
}
static inline int _kpj_scmp(kstr a, kstr b) {
    int64_t m = a.n < b.n ? a.n : b.n;
    for (int64_t i = 0; i < m; i++) {
        unsigned char x = (unsigned char)a.p[i], y = (unsigned char)b.p[i];
        if (x != y) return x < y ? -1 : 1;
    }
    return a.n < b.n ? -1 : (a.n > b.n ? 1 : 0);
}
/* __builtin_memcmp with constant length compiles to a single 16/32/64-bit register cmp! */
static __attribute__((always_inline)) inline int64_t _kpj_seq(kstr a, kstr b) {
    return a.n == b.n && (b.n == 0 || __builtin_memcmp(a.p, b.p, (size_t)b.n) == 0);
}
static inline kstr _kpj_str_int(KpjCtx *ctx, int64_t v) {
    char *b = (char *)_kpj_alloc(ctx, 24);
    return _kpj_mk(b, _kpj_fmt_int(b, v));
}
static inline kstr _kpj_str_float(KpjCtx *ctx, double v) {
    char *b = (char *)_kpj_alloc(ctx, 48);
    return _kpj_mk(b, _kpj_fmt_float(b, v));
}
static __attribute__((always_inline)) inline void _kpj_w_str(KpjCtx *ctx, kstr s) { _kpj_put(ctx, s.p, (size_t)s.n); }

static __attribute__((always_inline)) inline int _kpj_main(KpjCtx *ctx);

#ifdef KPJ_FREESTANDING
__attribute__((used, noreturn)) void _kpj_cstart(long *sp) {
    KpjCtx ctx;
    ctx.argc = (int)sp[0];
    ctx.nread = 0;
    ctx.argv = (char **)(sp + 1);
    ctx.outn = 0;
    ctx.inlen = 0;
    ctx.inpos = 0;
    ctx.loaded = 0;
    ctx.hoff = 0;
    __asm__ volatile ("" : : "r"(kopaj_flag) : "memory");
    _kpj_exit(&ctx, _kpj_main(&ctx));
}
__asm__(".text\n.globl _start\n.type _start,@function\n_start:\n"
        "  xor %ebp, %ebp\n  mov %rsp, %rdi\n  and $-16, %rsp\n  call _kpj_cstart\n  hlt\n");
#else
int main(int argc, char **argv) {
    KpjCtx ctx;
    ctx.argc = argc;
    ctx.nread = 0;
    ctx.argv = argv;
    ctx.outn = 0;
    ctx.inlen = 0;
    ctx.inpos = 0;
    ctx.loaded = 0;
    ctx.hoff = 0;
    if (kopaj_flag[0] == '\0') return 1;
    _kpj_exit(&ctx, _kpj_main(&ctx));
}
#endif

"##;
