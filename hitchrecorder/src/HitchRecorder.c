/*
 * HitchRecorder - RED4ext plugin for Cyberpunk 2077
 *
 * Read-only recorder. It changes nothing in the game: every hook calls the
 * original function with the original arguments and only notes how long it took.
 *
 * Records:
 *   - main loop frame times (RED4ext Running game state, every tick)
 *   - resource loading: IssueLoadingRequest, ResourceLoader::OnUpdate,
 *     ResourceSerializer::Load / Deserialize, StreamingSector::PostLoad, Entity::Attach
 *   - D3D12: pipeline (shader) creation, pipeline-library loads, resource/heap creation
 *   - REDscript time per mod: every scripted function the game runs is timed and charged
 *     to the mod whose .reds file it came from (exclusive time, 50 ms buckets). Our own code;
 *     the idea of mapping functions to source files at bind time is from jekky's redscript-dap (MIT).
 *   - CET Lua time per mod: every protected call CET makes into its LuaJIT (onUpdate,
 *     onDraw, Observe/Override handlers, hotkeys) is timed and charged to the mod that owns the
 *     called function. Our own code; LuaJIT and CET are MIT.
 *
 * Output (plugin folder\recordings):
 *   HitchRecorder_<date>.bin  32-byte records, for the analysis script
 *   HitchRecorder_<date>.log  per-minute summary + one line per spike frame
 *
 * Hashes/offsets: ArchiveXL 1.27.5 (game 2.31) and ArchiveXL master (2.5), RED4ext.SDK.
 * Licence of bundled MinHook: BSD-2 (see THIRD_PARTY.txt).
 */
#define WIN32_LEAN_AND_MEAN
#define COBJMACROS
#include <windows.h>
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "MinHook.h"

#define HR_VERSION_MAJOR 1
#define HR_VERSION_MINOR 0
#define HR_VERSION_PATCH 0
#define HR_STR2(x) #x
#define HR_STR(x) HR_STR2(x)
/* found by Night City Frame Lab to show which version is installed */
__attribute__((used)) static const char g_versionTag[] =
    "HITCHREC_VERSION=" HR_STR(HR_VERSION_MAJOR) "." HR_STR(HR_VERSION_MINOR) "." HR_STR(HR_VERSION_PATCH);

/* ------------------------------------------------------------------ */
/* RED4ext API v1 (C layout of RED4ext.SDK Api/v1)                     */
/* ------------------------------------------------------------------ */
typedef struct { uint32_t type, number; } R4PreRel;
typedef struct { uint8_t major; uint16_t minor; uint32_t patch; R4PreRel pre; } R4SemVer;
typedef struct { uint16_t major, minor, build, revision; } R4FileVer;
typedef struct {
    R4SemVer sdk;
    const wchar_t* name;
    const wchar_t* author;
    R4SemVer version;
    R4FileVer runtime;
} R4PluginInfo;
typedef void* R4Handle;
typedef struct {
    void (*f[8])(void);                                  /* Trace*, Debug* */
    void (*Info)(R4Handle, const char*);
    void (*InfoF)(R4Handle, const char*, ...);
    void (*InfoW)(R4Handle, const wchar_t*);
    void (*InfoWF)(R4Handle, const wchar_t*, ...);
    void (*Warn)(R4Handle, const char*);
    void (*WarnF)(R4Handle, const char*, ...);
    void (*WarnW)(R4Handle, const wchar_t*);
    void (*WarnWF)(R4Handle, const wchar_t*, ...);
    void (*Error)(R4Handle, const char*);
    void (*ErrorF)(R4Handle, const char*, ...);
    void (*g[6])(void);                                  /* ErrorW*, Critical* */
} R4Logger;
typedef struct {
    bool (*Attach)(R4Handle, void* target, void* detour, void** original);
    bool (*Detach)(R4Handle, void* target);
} R4Hooking;
typedef struct {
    bool (*OnEnter)(void* app);
    bool (*OnUpdate)(void* app);
    bool (*OnExit)(void* app);
} R4GameState;
typedef struct { bool (*Add)(R4Handle, uint32_t type, R4GameState* state); } R4GameStates;
typedef struct {
    R4SemVer* runtime;
    R4Logger* logger;
    R4Hooking* hooking;
    R4GameStates* gameStates;
    void* scripts;
} R4Sdk;
enum { R4_STATE_RUNNING = 2 };

/* ------------------------------------------------------------------ */
/* Record format                                                       */
/* ------------------------------------------------------------------ */
#pragma pack(push, 1)
typedef struct {
    uint64_t t_us;    /* start, microseconds since plugin load */
    uint64_t hash;    /* resource path hash / shader id / size, depends on kind */
    uint32_t frame;   /* main-loop frame number when the event finished */
    uint32_t dur_us;  /* duration */
    uint8_t  kind;
    uint8_t  flags;   /* bit0 main thread, bit1 call failed, bit2 cached PSO blob given */
    uint16_t extra;
    uint32_t tid;
} Rec;
typedef struct {
    char     magic[4];        /* "HREC" */
    uint32_t version;         /* 1 */
    uint64_t qpc_freq;
    uint64_t qpc0;
    uint64_t filetime0;       /* UTC FILETIME at qpc0 */
    uint16_t game_major, game_minor;
    uint32_t game_patch;
    uint32_t hooks;           /* bitmask of HOOK_* that were attached */
    uint32_t rec_size;        /* 32 */
    uint32_t spike_ms;
    uint32_t plugin_version;  /* major*10000+minor*100+patch */
    uint8_t  capture_mode;    /* 0 = records all the time, 1 = only between capture-key presses (MARK 4/5) */
    uint8_t  pad[7];
} Hdr;
#pragma pack(pop)
_Static_assert(sizeof(Rec) == 32, "Rec size");
_Static_assert(sizeof(Hdr) == 64, "Hdr size");

enum {
    K_FRAME = 0, K_LOAD = 1, K_LOADER_UPDATE = 2, K_DESER = 3, K_SECTOR = 4, K_ENTITY = 5, K_SERLOAD = 6,
    K_PSO_GFX = 10, K_PSO_COMP = 11, K_PSO_STREAM = 12, K_STATEOBJ = 13, K_ADDSTATEOBJ = 14, K_PSO_LIB = 15,
    K_RES_COMMIT = 20, K_RES_PLACED = 21, K_HEAP = 22,
    K_VRAM = 23,      /* hash = local VRAM in use (bytes), dur_us = budget (MB), extra = shared/system memory in use (MB) */
    K_MARK = 30,      /* extra: 1 running, 2 D3D12 hooks set, 3 mark key, 4 capture start, 5 capture stop, 6 focus start, 7 focus stop (session mode F11) */
    K_MAX = 32,
    /* script time per mod, one record per 50 ms bucket and owner: t_us = bucket start, hash = owner
       index (names in <recording>.owners.txt), dur_us = exclusive time in the bucket */
    K_SCRIPT = 40,        /* REDscript, all threads */
    K_SCRIPT_MAIN = 41,   /* REDscript, main thread only */
    K_CET = 42,           /* CET Lua, all threads (owner names "c<index>" in owners.txt) */
    K_CET_MAIN = 43,      /* CET Lua, main thread only */
};
static const char* g_kindName[K_MAX] = {
    [K_FRAME] = "FRAME", [K_LOAD] = "LOAD_REQ", [K_LOADER_UPDATE] = "LOADER_UPD", [K_DESER] = "DESERIALIZE",
    [K_SECTOR] = "SECTOR", [K_ENTITY] = "ENTITY", [K_SERLOAD] = "SER_LOAD",
    [K_PSO_GFX] = "PSO_GFX", [K_PSO_COMP] = "PSO_COMPUTE", [K_PSO_STREAM] = "PSO_STREAM",
    [K_STATEOBJ] = "RT_STATEOBJ", [K_ADDSTATEOBJ] = "RT_ADDSTATE", [K_PSO_LIB] = "PSO_LIBLOAD",
    [K_RES_COMMIT] = "RES_COMMIT", [K_RES_PLACED] = "RES_PLACED", [K_HEAP] = "HEAP", [K_VRAM] = "VRAM", [K_MARK] = "MARK",
};

enum {
    HOOK_LOADREQ = 1u << 0, HOOK_LOADERUPD = 1u << 1, HOOK_DESER = 1u << 2, HOOK_SERLOAD = 1u << 3,
    HOOK_SECTOR = 1u << 4, HOOK_ENTITY = 1u << 5, HOOK_FRAME = 1u << 6,
    HOOK_D3D_PSO = 1u << 8, HOOK_D3D_RES = 1u << 9, HOOK_D3D_LIB = 1u << 10, HOOK_D3D_RT = 1u << 11,
    HOOK_SCRIPT_BIND = 1u << 12, HOOK_SCRIPT_EXEC = 1u << 13, HOOK_CET = 1u << 14,
};

/* ------------------------------------------------------------------ */
/* Globals                                                             */
/* ------------------------------------------------------------------ */
static R4Handle g_handle;
static const R4Sdk* g_sdk;
static wchar_t g_dir[MAX_PATH];        /* plugin folder, with trailing backslash */
static LARGE_INTEGER g_freq, g_qpc0;
static volatile LONG g_frame;
static volatile DWORD g_mainTid;
static uint64_t g_lastTick;            /* QPC of previous Running OnUpdate */
static volatile uint32_t g_hooks;
static wchar_t g_binPath[MAX_PATH];      /* the .bin being written */

static struct {
    int enabled, loads, entities, gpuPso, gpuRes, spikeMs, maxMB, summarySec, markKey, captureMode, captureKey, vram, sessionOnly, scripts, cet;
} g_cfg = { 1, 1, 1, 1, 1, 50, 2048, 60, 0x91, 1, 0x7A, 1, 0, 1, 1 };
static volatile LONG g_capturing;      /* records are kept only while this is 1 (always 1 in capture mode 0) */

/* Live state for Night City Frame Lab (read-only for the app): Local\HitchRecorder.State */
#pragma pack(push, 8)
typedef struct {
    uint32_t magic;            /* 'HRST' */
    uint32_t version;          /* 1 */
    uint32_t pid;
    uint32_t pluginVersion;
    volatile LONG capturing;
    volatile LONG frame;
    volatile LONG spikes;      /* spike frames since the capture started */
    volatile LONG markers;
    uint64_t qpcFreq, qpc0, filetime0;
    volatile uint64_t lastFrameQpc;
    volatile float lastFrameMs;
    volatile float avgFrameMs; /* about the last second */
    volatile uint64_t vramUsage, vramBudget, sharedUsage;
    volatile uint32_t hooks;
    uint32_t captureMode;
    volatile uint64_t captureStartQpc, captureStopQpc;
    wchar_t binPath[260];
} SharedState;
#pragma pack(pop)
_Static_assert(offsetof(SharedState, lastFrameMs) == 64 && offsetof(SharedState, vramUsage) == 72 &&
               offsetof(SharedState, captureStartQpc) == 104 && offsetof(SharedState, binPath) == 120 &&
               sizeof(SharedState) == 640, "SharedState layout is shared with Frame Lab");
static SharedState* g_shared;
static HANDLE g_sharedMap;

/* double buffer. 0.3.1: 8x bigger (2 x 32 MB) and the writer is woken as soon as one half is half
   full: a loading screen can ask for well over 100,000 files within a second, and 0.3.0 lost ~40,000
   records at game start */
#define BUF_RECS (1u << 20)
static Rec g_buf[2][BUF_RECS];
static uint32_t g_cnt[2];
static int g_active;
static SRWLOCK g_lock = SRWLOCK_INIT;
static volatile LONG64 g_dropped;
static HANDLE g_writer, g_stop, g_wake;

/* add one record to the active half (caller checks g_capturing) */
static inline void buf_put(const Rec* r)
{
    AcquireSRWLockExclusive(&g_lock);
    int a = g_active;
    uint32_t n = g_cnt[a];
    int wake = 0;
    if (n < BUF_RECS) { g_buf[a][n] = *r; g_cnt[a] = n + 1; wake = (n + 1 == BUF_RECS / 2); }
    else g_dropped++;
    ReleaseSRWLockExclusive(&g_lock);
    if (wake && g_wake) SetEvent(g_wake);
}
static FILE* g_bin;
static FILE* g_log;

static inline uint64_t qpc(void) { LARGE_INTEGER x; QueryPerformanceCounter(&x); return (uint64_t)x.QuadPart; }
static inline uint64_t to_us(uint64_t ticks)
{
    uint64_t f = (uint64_t)g_freq.QuadPart;
    return (ticks / f) * 1000000ull + ((ticks % f) * 1000000ull) / f;
}

static void push(uint8_t kind, uint64_t hash, uint64_t t0, uint64_t t1, uint16_t extra, uint8_t flags)
{
    if (!g_capturing && kind != K_MARK) return;
    Rec r;
    DWORD tid = GetCurrentThreadId();
    r.t_us = to_us(t0 - (uint64_t)g_qpc0.QuadPart);
    r.hash = hash;
    r.frame = (uint32_t)g_frame;
    uint64_t d = to_us(t1 - t0);
    r.dur_us = d > 0xFFFFFFFFull ? 0xFFFFFFFFu : (uint32_t)d;
    r.kind = kind;
    r.flags = (uint8_t)(flags | (tid == g_mainTid ? 1 : 0));
    r.extra = extra;
    r.tid = tid;

    buf_put(&r);
}

/* ------------------------------------------------------------------ */
/* Engine hooks (RED4ext Hooking API, same targets ArchiveXL uses)      */
/* ------------------------------------------------------------------ */
#define H_LOADREQ        2365013187u  /* res::ResourceLoader::IssueLoadingRequest */
#define H_LOADERUPD_231  1303056161u  /* ResourceLoader::OnUpdate, 2.31 (ArchiveXL 1.27.5) */
#define H_LOADERUPD_25   3955625134u  /* ResourceLoader::OnUpdate, 2.5 (ArchiveXL 1.28) */
#define H_SERLOAD        2577814646u  /* ResourceSerializer::Load */
#define H_DESER          2901686778u  /* ResourceSerializer::Deserialize */
#define H_SECTOR         3972601611u  /* worldStreamingSector::PostLoad */
#define H_ENTITY         4248638169u  /* entEntity::Attach */

static inline uint64_t rd64(const void* p, size_t off) { return p ? *(const uint64_t*)((const uint8_t*)p + off) : 0; }
static inline uint8_t rd8(const void* p, size_t off) { return p ? *((const uint8_t*)p + off) : 0; }

/* IMPORTANT: every engine detour returns the original's RAX. IssueLoadingRequest really returns
   SharedPtr<ResourceToken> through a hidden pointer and its caller uses RAX straight after the call
   (Internal_LoadGatheredResources: call; mov rdx, rax). 0.1.0/0.2.0 dropped it -> crash at startup. */
typedef uintptr_t (*LoadReq_t)(void*, void*, void*);
static LoadReq_t oLoadReq;
static uintptr_t hkLoadReq(void* loader, void* token, void* req)
{
    uint64_t path = rd64(req, 0x00);          /* ResourceRequest::path */
    uint64_t t0 = qpc();
    uintptr_t ret = oLoadReq(loader, token, req);
    push(K_LOAD, path, t0, qpc(), 0, 0);
    return ret;
}

typedef uintptr_t (*LoaderUpd_t)(void*);
static LoaderUpd_t oLoaderUpd;
static uintptr_t hkLoaderUpd(void* loader)
{
    uint64_t t0 = qpc();
    uintptr_t ret = oLoaderUpd(loader);
    push(K_LOADER_UPDATE, 0, t0, qpc(), 0, 0);
    return ret;
}

typedef uintptr_t (*SerLoad_t)(void*, uint64_t, void*, uint64_t, uint64_t);
static SerLoad_t oSerLoad;
static uintptr_t hkSerLoad(void* s, uint64_t a2, void* req, uint64_t a4, uint64_t a5)
{
    uint64_t path = rd64(req, 0x28);          /* ResourceSerializerRequest::path */
    uint64_t t0 = qpc();
    uintptr_t ret = oSerLoad(s, a2, req, a4, a5);
    push(K_SERLOAD, path, t0, qpc(), 0, 0);
    return ret;
}

typedef uintptr_t (*Deser_t)(void*, uint64_t, uint64_t, void*, void*, uint64_t, void*, uint64_t);
static Deser_t oDeser;
static uintptr_t hkDeser(void* s, uint64_t a2, uint64_t a3, void* job, void* req, uint64_t a6, void* res, uint64_t a8)
{
    uint64_t path = rd64(req, 0x28);
    uint64_t t0 = qpc();
    uintptr_t ret = oDeser(s, a2, a3, job, req, a6, res, a8);
    push(K_DESER, path, t0, qpc(), 0, 0);
    return ret;
}

typedef uintptr_t (*Sector_t)(void*, uint64_t);
static Sector_t oSector;
static uintptr_t hkSector(void* sector, uint64_t a2)
{
    uint64_t path = rd64(sector, 0x30);       /* CResource::path */
    uint16_t lvl = (uint16_t)(rd8(sector, 0xE0) | (rd8(sector, 0xE1) << 8)); /* level, category */
    uint64_t t0 = qpc();
    uintptr_t ret = oSector(sector, a2);
    push(K_SECTOR, path, t0, qpc(), lvl, 0);
    return ret;
}

typedef uintptr_t (*EntAttach_t)(void*, uintptr_t);
static EntAttach_t oEntAttach;
static uintptr_t hkEntAttach(void* ent, uintptr_t a2)
{
    uint64_t tpl = rd64(ent, 0x60);           /* template path (offset unverified on 2.31; analysis checks it) */
    uint64_t t0 = qpc();
    uintptr_t ret = oEntAttach(ent, a2);
    push(K_ENTITY, tpl, t0, qpc(), 0, 0);
    return ret;
}

/* ------------------------------------------------------------------ */
/* REDscript time per mod                                               */
/* ------------------------------------------------------------------ */
/* At load the game binds every scripted function and passes its source file (FunctionInfo +0x18
   function, +0xC0 SourceFileInfo, whose path is a CString at +0x28). The owner of a function is
   the mod folder under r6\scripts (or the file name if it sits directly in r6\scripts); the
   game's own scripts are owner 0. Every scripted function body then runs through one bytecode
   interpreter entry, bool Execute(function, frame, result, resultType), hooked here: time spent in
   a function minus the time of the script functions it calls is charged to its owner (native
   functions it calls count as its own time). Only on while scripts are being recorded (F11 parts
   in session mode), otherwise the hook is a flag test and a jump. */
#define H_BINDFUNC   777921665u   /* binds a script function to its source file (redscript-dap uses the same) */
#define H_SCRIPTEXEC 588193222u   /* bytecode interpreter entry for one scripted function body (2.31) */

#define MAX_OWNERS 2048
#define OWNER_NAME 64
static char g_ownerName[MAX_OWNERS][OWNER_NAME];
static volatile LONG g_owners = 2;            /* 0 game scripts, 1 unknown source */
static SRWLOCK g_ownerLock = SRWLOCK_INIT;
static volatile LONG g_ownersWritten;
static volatile LONG g_bound, g_boundMods;

#define FMAP_BITS 19
static void* volatile g_fkey[1u << FMAP_BITS];
static uint16_t g_fowner[1u << FMAP_BITS];
static inline uint32_t fslot(const void* f) { return (uint32_t)((((uintptr_t)f >> 4) * 0x9E3779B97F4A7C15ull) >> (64 - FMAP_BITS)); }
static void fmap_put(void* f, uint16_t owner)
{
    uint32_t m = (1u << FMAP_BITS) - 1, i = fslot(f);
    for (uint32_t n = 0; n <= m; ++n, i = (i + 1) & m) {
        void* k = g_fkey[i];
        if (k == f) { g_fowner[i] = owner; return; }
        if (!k) { g_fowner[i] = owner; g_fkey[i] = f; return; }   /* owner first, then key (one writer: binding) */
    }
}
static inline uint16_t fmap_get(const void* f)
{
    uint32_t m = (1u << FMAP_BITS) - 1, i = fslot(f);
    for (uint32_t n = 0; n < 64; ++n, i = (i + 1) & m) {
        void* k = g_fkey[i];
        if (k == f) return g_fowner[i];
        if (!k) break;
    }
    return 1;
}

/* bind time only (~100k calls at load): never trust a pointer from a struct layout we can't see */
static int readable(const void* p, size_t n) { return p && !IsBadReadPtr(p, n); }
static const char* cstring_at(const void* p, size_t off)
{
    if (!readable(p, off + 0x20)) return NULL;
    const uint8_t* s = (const uint8_t*)p + off;
    uint32_t len = *(const uint32_t*)(s + 0x14);
    const char* t = len < 0x40000000u ? (const char*)s : *(const char* const*)s;
    uint32_t n = len & 0x3FFFFFFFu;
    if (!t || n > 1024 || !readable(t, n + 1)) return NULL;
    return t;
}

static uint16_t owner_index(const char* name)
{
    AcquireSRWLockExclusive(&g_ownerLock);
    LONG n = g_owners;
    for (LONG i = 2; i < n; ++i)
        if (!_stricmp(g_ownerName[i], name)) { ReleaseSRWLockExclusive(&g_ownerLock); return (uint16_t)i; }
    uint16_t idx = 1;
    if (n < MAX_OWNERS) {
        snprintf(g_ownerName[n], OWNER_NAME, "%s", name);
        idx = (uint16_t)n;
        g_owners = n + 1;
        g_boundMods++;
    }
    ReleaseSRWLockExclusive(&g_ownerLock);
    return idx;
}

/* "...\r6\scripts\NightCityExchange\NCX.Site.reds" -> "NightCityExchange"; "...\r6\scripts\One.reds" -> "One";
   the game's own script paths (relative, no r6\scripts) -> 0 */
static uint16_t owner_of_path(const char* path)
{
    if (!path || !*path) return 1;
    const char* p = NULL;
    for (const char* q = path; *q; ++q) {
        if ((q[0] == 'r' || q[0] == 'R') && q[1] == '6' && (q[2] == '\\' || q[2] == '/') && !_strnicmp(q + 3, "scripts", 7) && (q[10] == '\\' || q[10] == '/')) {
            p = q + 11;
        }
    }
    if (!p) return 0;
    char name[OWNER_NAME];
    size_t n = 0;
    while (p[n] && p[n] != '\\' && p[n] != '/' && n < OWNER_NAME - 1) n++;
    memcpy(name, p, n); name[n] = 0;
    if (!p[n]) {                                    /* file directly in r6\scripts: drop .reds */
        char* dot = strrchr(name, '.');
        if (dot && !_stricmp(dot, ".reds")) *dot = 0;
    }
    return name[0] ? owner_index(name) : 1;
}

typedef uintptr_t (*Bind_t)(void*, void*, void*);
static Bind_t oBind;
static uintptr_t hkBind(void* self, void* info, void* arg)
{
    uintptr_t r = oBind(self, info, arg);
    if (!readable(info, 0xD0)) return r;
    void* func = (void*)rd64(info, 0x18);
    const void* src = (const void*)rd64(info, 0xC0);
    if (func && src) {
        fmap_put(func, owner_of_path(cstring_at(src, 0x28)));
        InterlockedIncrement(&g_bound);
    }
    return r;
}

/* per thread: the script functions being timed, REDscript (layer 0) and CET Lua (layer 1) on one
   stack, so a second is never counted twice */
typedef struct { uint64_t t0, child; uint16_t owner, layer; } SFrame;
typedef struct { int depth; SFrame f[160]; } SStack;
static DWORD g_tls = TLS_OUT_OF_INDEXES;
static volatile LONG g_scriptsOn;

/* 50 ms buckets in a ring of 16; the writer flushes buckets that are at least 2 old */
#define SB_SLOTS 16
static uint64_t g_sbTicks;                       /* QPC ticks per bucket */
static volatile LONG64 g_sbAll[SB_SLOTS][MAX_OWNERS];
static volatile LONG64 g_sbMain[SB_SLOTS][MAX_OWNERS];
static volatile LONG64 g_sbUsed[SB_SLOTS];       /* bucket number + 1 last written into the slot */
static int64_t g_sbFlushed = -1;

typedef uintptr_t (*Exec_t)(void*, void*, void*, void*);
static Exec_t oExec;
static SStack* sstack(void)
{
    SStack* s = (SStack*)TlsGetValue(g_tls);
    if (!s) {
        s = (SStack*)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof *s);
        if (s) TlsSetValue(g_tls, s);
    }
    return s;
}
/* end of a timed call: its time minus the timed calls inside it goes to its owner; the depth is set
   back to the call's own slot, so the stack can never drift */
static inline uint64_t sframe_end(SStack* s, int d, uint64_t t1)
{
    SFrame* fr = &s->f[d];
    uint64_t incl = t1 - fr->t0, excl = incl > fr->child ? incl - fr->child : 0;
    s->depth = d;
    if (d > 0) s->f[d - 1].child += incl;
    return excl;
}
static inline void sb_add(volatile LONG64 (*all)[MAX_OWNERS], volatile LONG64 (*mainT)[MAX_OWNERS], uint16_t owner, uint64_t t1, uint64_t excl)
{
    uint64_t b = (t1 - (uint64_t)g_qpc0.QuadPart) / g_sbTicks;
    int slot = (int)(b % SB_SLOTS);
    InterlockedAdd64(&all[slot][owner], (LONG64)excl);
    if (GetCurrentThreadId() == g_mainTid) InterlockedAdd64(&mainT[slot][owner], (LONG64)excl);
    if (g_sbUsed[slot] != (LONG64)b + 1) InterlockedExchange64(&g_sbUsed[slot], (LONG64)b + 1);
}
static uintptr_t hkExec(void* func, void* frame, void* result, void* rtype)
{
    if (!g_scriptsOn) return oExec(func, frame, result, rtype);
    SStack* s = sstack();
    if (!s || s->depth >= 160) return oExec(func, frame, result, rtype);
    uint16_t owner = fmap_get(func);
    /* the game's own script code run from a CET mod's Lua (Game.X(), Observe/Override) counts for that mod */
    if (owner <= 1 && s->depth > 0 && s->f[s->depth - 1].layer == 1) return oExec(func, frame, result, rtype);
    int d = s->depth++;
    SFrame* fr = &s->f[d];
    fr->owner = owner; fr->layer = 0; fr->child = 0;
    fr->t0 = qpc();
    uintptr_t r = oExec(func, frame, result, rtype);
    uint64_t t1 = qpc();
    sb_add(g_sbAll, g_sbMain, owner, t1, sframe_end(s, d, t1));
    return r;
}

/* ------------------------------------------------------------------ */
/* CET Lua time per mod                                                 */
/* ------------------------------------------------------------------ */
/* CET calls into its Lua for every mod callback (onUpdate, onDraw, Observe/Override handlers, hotkeys)
   as a protected call, and every protected call ends in LuaJIT's lj_vm_pcall(L, base, nres1, ef).
   That one function is hooked inside cyber_engine_tweaks.asi, found by its machine code (LuaJIT 2.1
   GC64 as CET 1.3x ships it; nothing is hooked if the code doesn't match). The called function sits
   two slots below base. CET gives every mod its own environment table and every function a mod
   defines shares it, so the environment tells the mod. Which mod an environment belongs to is learnt
   when CET runs that mod's init.lua: CET switches the working folder to the mod's folder for that
   (a main chunk: its prototype starts at line 0). Lua the game runs from CET's console or CET's own
   scripts counts as "CET itself". Game script code a CET mod calls counts for that mod; .reds mod code
   it triggers stays with that .reds mod. */
#define MAX_CET 256
static char g_cetName[MAX_CET][OWNER_NAME];
static volatile LONG g_cetOwners = 1;            /* 0 = CET itself */
static volatile LONG g_cetLearnt;
static volatile LONG64 g_cbAll[SB_SLOTS][MAX_OWNERS];   /* only the first MAX_CET owners are used */
static volatile LONG64 g_cbMain[SB_SLOTS][MAX_OWNERS];
#define EMAP_BITS 10
static void* volatile g_ekey[1u << EMAP_BITS];
static volatile uint16_t g_eowner[1u << EMAP_BITS];
static SRWLOCK g_emapLock = SRWLOCK_INIT;
static inline uint32_t eslot(const void* e) { return (uint32_t)((((uintptr_t)e >> 3) * 0x9E3779B97F4A7C15ull) >> (64 - EMAP_BITS)); }
static inline uint16_t emap_get(const void* e)
{
    uint32_t m = (1u << EMAP_BITS) - 1, i = eslot(e);
    for (uint32_t n = 0; n < 32; ++n, i = (i + 1) & m) {
        void* k = g_ekey[i];
        if (k == e) return g_eowner[i];
        if (!k) break;
    }
    return 0;
}
static void emap_put(void* e, uint16_t owner)
{
    AcquireSRWLockExclusive(&g_emapLock);
    uint32_t m = (1u << EMAP_BITS) - 1, i = eslot(e);
    for (uint32_t n = 0; n < 32; ++n, i = (i + 1) & m) {
        void* k = g_ekey[i];
        if (k == e) { g_eowner[i] = owner; break; }
        if (!k) { g_eowner[i] = owner; MemoryBarrier(); g_ekey[i] = e; break; }
    }
    ReleaseSRWLockExclusive(&g_emapLock);
}
static uint16_t cet_owner_index(const char* name)
{
    AcquireSRWLockExclusive(&g_ownerLock);
    LONG n = g_cetOwners;
    for (LONG i = 1; i < n; ++i)
        if (!_stricmp(g_cetName[i], name)) { ReleaseSRWLockExclusive(&g_ownerLock); return (uint16_t)i; }
    uint16_t idx = 0;
    if (n < MAX_CET) {
        snprintf(g_cetName[n], OWNER_NAME, "%s", name);
        idx = (uint16_t)n;
        g_cetOwners = n + 1;
    }
    ReleaseSRWLockExclusive(&g_ownerLock);
    return idx;
}
/* a main chunk is starting: if the working folder is "...\mods\<name>", the environment is that mod's */
static void cet_learn(void* env)
{
    wchar_t cwd[MAX_PATH * 2];
    DWORD n = GetCurrentDirectoryW(MAX_PATH * 2, cwd);
    if (!n || n >= MAX_PATH * 2) return;
    while (n > 3 && (cwd[n - 1] == L'\\' || cwd[n - 1] == L'/')) cwd[--n] = 0;
    wchar_t* leaf = NULL; wchar_t* parent = NULL;
    for (wchar_t* q = cwd; *q; ++q)
        if (*q == L'\\' || *q == L'/') { parent = leaf; leaf = q; }
    if (!leaf || !parent || leaf - parent != 5 || _wcsnicmp(parent + 1, L"mods", 4)) return;
    char name[OWNER_NAME];
    if (!WideCharToMultiByte(CP_UTF8, 0, leaf + 1, -1, name, sizeof name, NULL, NULL) || !name[0]) return;
    uint16_t o = cet_owner_index(name);
    if (o && emap_get(env) != o) { emap_put(env, o); InterlockedIncrement(&g_cetLearnt); }
}

/* LuaJIT GC64 layout: TValue = 8 bytes, a function's tag is -9 in the top 17 bits; GCfunc: +0x0A ffid
   (0 = Lua), +0x10 env, +0x20 pc (= its GCproto + 0x68); GCproto: +0x48 firstline */
typedef uintptr_t (*VmPcall_t)(void*, void*, uintptr_t, uintptr_t);
static VmPcall_t oPcall;
static uintptr_t hkPcall(void* L, void* base, uintptr_t nres1, uintptr_t ef)
{
    uint64_t tv = *(const uint64_t*)((const uint8_t*)base - 16);
    void* env = NULL;
    if ((int64_t)tv >> 47 == -9) {
        const uint8_t* fn = (const uint8_t*)(uintptr_t)(tv & ((1ull << 47) - 1));
        if (fn[0x0A] == 0) {
            env = *(void* const*)(fn + 0x10);
            const uint8_t* pc = *(const uint8_t* const*)(fn + 0x20);
            if (env && pc && *(const int32_t*)(pc - 0x68 + 0x48) == 0) cet_learn(env);
        }
    }
    if (!g_scriptsOn) return oPcall(L, base, nres1, ef);
    SStack* s = sstack();
    if (!s || s->depth >= 160) return oPcall(L, base, nres1, ef);
    uint16_t owner = env ? emap_get(env) : 0;
    int d = s->depth++;
    SFrame* fr = &s->f[d];
    fr->owner = owner; fr->layer = 1; fr->child = 0;
    fr->t0 = qpc();
    uintptr_t r = oPcall(L, base, nres1, ef);
    uint64_t t1 = qpc();
    sb_add(g_cbAll, g_cbMain, owner, t1, sframe_end(s, d, t1));
    return r;
}

static const uint8_t* find_bytes(const uint8_t* p, const uint8_t* end, const uint8_t* pat, size_t n)
{
    for (; p + n <= end; ++p) {
        p = (const uint8_t*)memchr(p, pat[0], (size_t)(end - p));
        if (!p || p + n > end) return NULL;
        if (!memcmp(p, pat, n)) return p;
    }
    return NULL;
}
/* find lj_vm_pcall in CET and hook it (MinHook, initialised by the caller). Tried at load (CET's
   loader runs before RED4ext's plugins) and once more when the game reaches Running. */
static int g_cetMissing;
static void hook_cet(void)
{
    HMODULE m = GetModuleHandleW(L"cyber_engine_tweaks.asi");
    g_cetMissing = !m;
    if (!m) { g_sdk->logger->Info(g_handle, "CET not loaded (yet): no CET Lua timing"); return; }
    const IMAGE_DOS_HEADER* dos = (const IMAGE_DOS_HEADER*)m;
    const IMAGE_NT_HEADERS64* nt = (const IMAGE_NT_HEADERS64*)((const uint8_t*)m + dos->e_lfanew);
    const IMAGE_SECTION_HEADER* sec = IMAGE_FIRST_SECTION(nt);
    const uint8_t *text = NULL, *end = NULL;
    for (unsigned i = 0; i < nt->FileHeader.NumberOfSections; ++i)
        if (!memcmp(sec[i].Name, ".text", 6)) { text = (const uint8_t*)m + sec[i].VirtualAddress; end = text + sec[i].Misc.VirtualSize; }
    if (!text) return;
    /* vm_pcall: push rbp/rdi/rsi/rbx; sub rsp,28h; mov esi,FRAME_CP(5); mov [rsp+54h],r9d; jmp; then vm_call
       (same start, mov esi,FRAME_C(1); mov [rsp+50h],r8d), which loads the function from [base-10h] and its pc
       from [fn+20h]; elsewhere the VM reads GCproto.k at pc-4Ch (sizeof GCproto = 68h) */
    static const uint8_t pcallSig[] = { 0x55,0x57,0x56,0x53, 0x48,0x83,0xEC,0x28, 0xBE,0x05,0,0,0, 0x44,0x89,0x4C,0x24,0x54, 0xEB };
    static const uint8_t callSig[]  = { 0x55,0x57,0x56,0x53, 0x48,0x83,0xEC,0x28, 0xBE,0x01,0,0,0, 0x44,0x89,0x44,0x24,0x50 };
    static const uint8_t ldFunc[] = { 0x48,0x8B,0x69,0xF0 }, ldPc[] = { 0x48,0x8B,0x75,0x20 }, ldK[] = { 0x48,0x8B,0x7E,0xB4 };
    const uint8_t* p = find_bytes(text, end, pcallSig, sizeof pcallSig);
    const char* why = NULL;
    if (!p) why = "lj_vm_pcall not found";
    else if (find_bytes(p + 19, end, pcallSig, sizeof pcallSig)) why = "lj_vm_pcall found twice";
    else if (p + 20 + sizeof callSig > end || memcmp(p + 20, callSig, sizeof callSig)) why = "lj_vm_call not next to it";
    else if (!find_bytes(p + 20, p + 20 + 0xA0, ldFunc, 4) || !find_bytes(p + 20, p + 20 + 0xA0, ldPc, 4)) why = "unexpected stack layout";
    else if (!find_bytes(text, p, ldK, 4) && !find_bytes(p, p + 0x4000 < end ? p + 0x4000 : end, ldK, 4)) why = "unexpected prototype layout";
    if (why) { g_sdk->logger->WarnF(g_handle, "CET found but %s (other LuaJIT build?): no CET Lua timing", why); return; }
    if (MH_CreateHook((void*)p, (void*)hkPcall, (LPVOID*)&oPcall) != MH_OK || MH_EnableHook((void*)p) != MH_OK) {
        g_sdk->logger->Warn(g_handle, "CET lj_vm_pcall hook failed: no CET Lua timing");
        return;
    }
    g_hooks |= HOOK_CET;
    g_sdk->logger->InfoF(g_handle, "CET Lua timing hooked (lj_vm_pcall at asi+0x%llx)", (unsigned long long)(p - (const uint8_t*)m));
}

static void script_set(int on)
{
    if (!(g_hooks & (HOOK_SCRIPT_EXEC | HOOK_CET))) return;
    InterlockedExchange(&g_scriptsOn, on ? 1 : 0);
}

static void rec_raw(uint8_t kind, uint64_t t_us, uint64_t hash, uint64_t dur_us)
{
    Rec r;
    r.t_us = t_us; r.hash = hash; r.frame = (uint32_t)g_frame;
    r.dur_us = dur_us > 0xFFFFFFFFull ? 0xFFFFFFFFu : (uint32_t)dur_us;
    r.kind = kind; r.flags = 0; r.extra = 0; r.tid = 0;
    buf_put(&r);
}

static void owners_write(void);
/* writer thread: move finished buckets into the recording */
static void script_flush(int all)
{
    if (!g_sbTicks) return;
    int64_t cur = (int64_t)((qpc() - (uint64_t)g_qpc0.QuadPart) / g_sbTicks);
    int64_t upto = all ? cur : cur - 2;
    if (g_sbFlushed < upto - SB_SLOTS + 1) g_sbFlushed = upto - SB_SLOTS;  /* writer fell behind: skip what was overwritten */
    LONG no = g_owners;
    for (int64_t b = g_sbFlushed + 1; b <= upto; ++b) {
        int slot = (int)(((b % SB_SLOTS) + SB_SLOTS) % SB_SLOTS);
        if (g_sbUsed[slot] != b + 1) continue;
        uint64_t t_us = to_us((uint64_t)b * g_sbTicks);
        for (LONG o = 0; o < no; ++o) {
            LONG64 a = InterlockedExchange64(&g_sbAll[slot][o], 0);
            LONG64 m = InterlockedExchange64(&g_sbMain[slot][o], 0);
            if (a > 0) rec_raw(K_SCRIPT, t_us, (uint64_t)o, to_us((uint64_t)a));
            if (m > 0) rec_raw(K_SCRIPT_MAIN, t_us, (uint64_t)o, to_us((uint64_t)m));
        }
        LONG nc = g_cetOwners;
        for (LONG o = 0; o < nc; ++o) {
            LONG64 a = InterlockedExchange64(&g_cbAll[slot][o], 0);
            LONG64 m = InterlockedExchange64(&g_cbMain[slot][o], 0);
            if (a > 0) rec_raw(K_CET, t_us, (uint64_t)o, to_us((uint64_t)a));
            if (m > 0) rec_raw(K_CET_MAIN, t_us, (uint64_t)o, to_us((uint64_t)m));
        }
    }
    if (upto > g_sbFlushed) g_sbFlushed = upto;
    if (g_owners + g_cetOwners != g_ownersWritten) owners_write();
}

/* <recording>.owners.txt: index <tab> owner, read by Night City Frame Lab */
static void owners_write(void)
{
    if (!g_binPath[0]) return;
    wchar_t p[MAX_PATH + 16];
    _snwprintf(p, MAX_PATH + 16, L"%.*ls.owners.txt", (int)(wcslen(g_binPath) - 4), g_binPath);
    FILE* f = _wfopen(p, L"w");
    if (!f) return;
    LONG n = g_owners, nc = g_cetOwners;
    fprintf(f, "0\tgame scripts\n1\tscripts without a source file\n");
    for (LONG i = 2; i < n; ++i) fprintf(f, "%ld\t%s\n", (long)i, g_ownerName[i]);
    fprintf(f, "c0\tCET itself\n");
    for (LONG i = 1; i < nc; ++i) fprintf(f, "c%ld\t%s\n", (long)i, g_cetName[i]);
    fclose(f);
    g_ownersWritten = n + nc;
}

/* ------------------------------------------------------------------ */
/* D3D12 hooks (MinHook on the device's vtable functions)              */
/* ------------------------------------------------------------------ */
typedef uintptr_t U;
typedef HRESULT (WINAPI *Fn4)(U, U, U, U);
typedef HRESULT (WINAPI *Fn5)(U, U, U, U, U);
typedef HRESULT (WINAPI *Fn8)(U, U, U, U, U, U, U, U);
typedef HRESULT (WINAPI *Fn9)(U, U, U, U, U, U, U, U, U);
typedef HRESULT (WINAPI *Fn10)(U, U, U, U, U, U, U, U, U, U);
typedef HRESULT (WINAPI *Fn11)(U, U, U, U, U, U, U, U, U, U, U);

/* shader id = first 8 bytes of the DXBC/DXIL container digest */
static uint64_t shader_id(const void* desc, size_t off)
{
    const uint8_t* bc = (const uint8_t*)rd64(desc, off);
    uint64_t len = rd64(desc, off + 8);
    if (!bc || len < 20) return 0;
    if (memcmp(bc, "DXBC", 4) != 0) return 0;
    uint64_t id; memcpy(&id, bc + 4, 8);
    return id;
}
static inline uint64_t rotl1(uint64_t x) { return (x << 1) | (x >> 63); }

static uint64_t res_key(const void* rdesc, uint16_t* extra)
{
    if (!rdesc) { *extra = 0; return 0; }
    uint32_t dim = *(const uint32_t*)rdesc;
    uint64_t w = rd64(rdesc, 16);
    uint32_t h = *(const uint32_t*)((const uint8_t*)rdesc + 24);
    uint16_t mips = *(const uint16_t*)((const uint8_t*)rdesc + 30);
    uint32_t fmt = *(const uint32_t*)((const uint8_t*)rdesc + 32);
    if (mips > 31) mips = 31;
    *extra = (uint16_t)((dim & 7) | (mips << 3) | ((fmt & 0xFF) << 8));
    return (w & 0xFFFFFFFFFFull) | ((uint64_t)(h > 0xFFFFFF ? 0xFFFFFF : h) << 40);
}

static Fn4 oGfx4, oComp4, oStream4, oStateObj4, oHeap4;
static Fn5 oAddState5, oHeap1_5, oLibGfx5, oLibComp5, oLib1Load5;
static Fn8 oCommit8, oPlaced8, oPlaced1_8;
static Fn9 oCommit1_9, oCommit2_9;
static Fn10 oPlaced2_10;
static Fn11 oCommit3_11;

static HRESULT WINAPI hkGfx(U self, U desc, U riid, U pp)
{
    const void* d = (const void*)desc;
    uint64_t id = shader_id(d, 8) ^ rotl1(shader_id(d, 24));
    uint8_t fl = (d && rd64(d, 632 + 8) > 0) ? 4 : 0;   /* CachedPSO.CachedBlobSizeInBytes */
    uint64_t t0 = qpc();
    HRESULT hr = oGfx4(self, desc, riid, pp);
    push(K_PSO_GFX, id, t0, qpc(), 0, (uint8_t)(fl | (FAILED(hr) ? 2 : 0)));
    return hr;
}
static HRESULT WINAPI hkComp(U self, U desc, U riid, U pp)
{
    const void* d = (const void*)desc;
    uint64_t id = shader_id(d, 8);
    uint8_t fl = (d && rd64(d, 32 + 8) > 0) ? 4 : 0;
    uint64_t t0 = qpc();
    HRESULT hr = oComp4(self, desc, riid, pp);
    push(K_PSO_COMP, id, t0, qpc(), 0, (uint8_t)(fl | (FAILED(hr) ? 2 : 0)));
    return hr;
}
static HRESULT WINAPI hkStream(U self, U desc, U riid, U pp)
{
    uint64_t sz = rd64((const void*)desc, 0);
    uint64_t t0 = qpc();
    HRESULT hr = oStream4(self, desc, riid, pp);
    push(K_PSO_STREAM, 0, t0, qpc(), (uint16_t)(sz > 0xFFFF ? 0xFFFF : sz), FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkStateObj(U self, U desc, U riid, U pp)
{
    uint32_t n = desc ? *(const uint32_t*)((const uint8_t*)desc + 4) : 0;
    uint64_t t0 = qpc();
    HRESULT hr = oStateObj4(self, desc, riid, pp);
    push(K_STATEOBJ, 0, t0, qpc(), (uint16_t)(n > 0xFFFF ? 0xFFFF : n), FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkAddState(U self, U desc, U grow, U riid, U pp)
{
    uint32_t n = desc ? *(const uint32_t*)((const uint8_t*)desc + 4) : 0;
    uint64_t t0 = qpc();
    HRESULT hr = oAddState5(self, desc, grow, riid, pp);
    push(K_ADDSTATEOBJ, 0, t0, qpc(), (uint16_t)(n > 0xFFFF ? 0xFFFF : n), FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkHeap(U self, U desc, U riid, U pp)
{
    uint64_t sz = rd64((const void*)desc, 0);
    uint64_t t0 = qpc();
    HRESULT hr = oHeap4(self, desc, riid, pp);
    push(K_HEAP, sz, t0, qpc(), 0, FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkHeap1(U self, U desc, U prot, U riid, U pp)
{
    uint64_t sz = rd64((const void*)desc, 0);
    uint64_t t0 = qpc();
    HRESULT hr = oHeap1_5(self, desc, prot, riid, pp);
    push(K_HEAP, sz, t0, qpc(), 1, FAILED(hr) ? 2 : 0);
    return hr;
}
/* resources: hash = width | height<<40, extra = dim | mips<<3 | format<<8 */
#define RES_BODY(KIND, DESC, CALL) \
    uint16_t ex; uint64_t key = res_key((const void*)(DESC), &ex); \
    uint64_t t0 = qpc(); HRESULT hr = CALL; \
    push(KIND, key, t0, qpc(), ex, FAILED(hr) ? 2 : 0); return hr;

static HRESULT WINAPI hkCommit(U s, U hp, U hf, U d, U st, U cv, U riid, U pp)
{ RES_BODY(K_RES_COMMIT, d, oCommit8(s, hp, hf, d, st, cv, riid, pp)) }
static HRESULT WINAPI hkCommit1(U s, U hp, U hf, U d, U st, U cv, U ps, U riid, U pp)
{ RES_BODY(K_RES_COMMIT, d, oCommit1_9(s, hp, hf, d, st, cv, ps, riid, pp)) }
static HRESULT WINAPI hkCommit2(U s, U hp, U hf, U d, U st, U cv, U ps, U riid, U pp)
{ RES_BODY(K_RES_COMMIT, d, oCommit2_9(s, hp, hf, d, st, cv, ps, riid, pp)) }
static HRESULT WINAPI hkCommit3(U s, U hp, U hf, U d, U lay, U cv, U ps, U nf, U fmts, U riid, U pp)
{ RES_BODY(K_RES_COMMIT, d, oCommit3_11(s, hp, hf, d, lay, cv, ps, nf, fmts, riid, pp)) }
static HRESULT WINAPI hkPlaced(U s, U heap, U off, U d, U st, U cv, U riid, U pp)
{ RES_BODY(K_RES_PLACED, d, oPlaced8(s, heap, off, d, st, cv, riid, pp)) }
static HRESULT WINAPI hkPlaced1(U s, U heap, U off, U d, U st, U cv, U riid, U pp)
{ RES_BODY(K_RES_PLACED, d, oPlaced1_8(s, heap, off, d, st, cv, riid, pp)) }
static HRESULT WINAPI hkPlaced2(U s, U heap, U off, U d, U lay, U cv, U nf, U fmts, U riid, U pp)
{ RES_BODY(K_RES_PLACED, d, oPlaced2_10(s, heap, off, d, lay, cv, nf, fmts, riid, pp)) }

/* pipeline library loads: hit = found in the game's PSO cache, flags bit1 = miss (E_INVALIDARG) */
static uint64_t name_hash(const wchar_t* s)
{
    uint64_t h = 1469598103934665603ull;
    if (!s) return 0;
    for (; *s; ++s) { h ^= (uint16_t)*s; h *= 1099511628211ull; }
    return h;
}
static HRESULT WINAPI hkLibGfx(U self, U name, U desc, U riid, U pp)
{
    uint64_t t0 = qpc();
    HRESULT hr = oLibGfx5(self, name, desc, riid, pp);
    push(K_PSO_LIB, name_hash((const wchar_t*)name), t0, qpc(), 0, FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkLibComp(U self, U name, U desc, U riid, U pp)
{
    uint64_t t0 = qpc();
    HRESULT hr = oLibComp5(self, name, desc, riid, pp);
    push(K_PSO_LIB, name_hash((const wchar_t*)name), t0, qpc(), 1, FAILED(hr) ? 2 : 0);
    return hr;
}
static HRESULT WINAPI hkLib1Load(U self, U name, U desc, U riid, U pp)
{
    uint64_t t0 = qpc();
    HRESULT hr = oLib1Load5(self, name, desc, riid, pp);
    push(K_PSO_LIB, name_hash((const wchar_t*)name), t0, qpc(), 2, FAILED(hr) ? 2 : 0);
    return hr;
}

static const GUID kIID_IDXGIFactory4 = {0x1bc6ea02, 0xef36, 0x464f, {0xbf, 0x0c, 0x21, 0xca, 0x39, 0xe5, 0x16, 0x8a}};
static const GUID kIID_IDXGIAdapter1 = {0x29038f61, 0x3839, 0x4626, {0x91, 0xfd, 0x08, 0x68, 0x79, 0x01, 0x1a, 0x05}};
static const GUID kCLSID_DeviceFactory = {0x114863bf, 0xc386, 0x4aee, {0xb3, 0x9d, 0x8f, 0x0b, 0xbb, 0x06, 0x29, 0x55}};
static const GUID kIID_DeviceFactory = {0x61f307d3, 0xd34e, 0x4e7c, {0x83, 0x74, 0x3b, 0xa4, 0xde, 0x23, 0xcc, 0xcb}};
static const GUID kIID_Dev  = {0x189819f1, 0x1db6, 0x4b57, {0xbe, 0x54, 0x18, 0x21, 0x33, 0x9b, 0x85, 0xf7}};
static const GUID kIID_Dev1 = {0x77acce80, 0x638e, 0x4e65, {0x88, 0x95, 0xc1, 0xf2, 0x33, 0x86, 0x86, 0x3e}};
static const GUID kIID_Dev2 = {0x30baa41e, 0xb15b, 0x475c, {0xa0, 0xbb, 0x1a, 0xf5, 0xc5, 0xb6, 0x43, 0x28}};
static const GUID kIID_Dev4 = {0xe865df17, 0xa9ee, 0x46f9, {0xa4, 0x63, 0x30, 0x98, 0x31, 0x5a, 0xa2, 0xe5}};
static const GUID kIID_Dev5 = {0x8b4f173b, 0x2fea, 0x4b80, {0x8f, 0x58, 0x43, 0x07, 0x19, 0x1a, 0xb9, 0x5d}};
static const GUID kIID_Dev7 = {0x5c014b53, 0x68a1, 0x4b9b, {0x8b, 0xd1, 0xdd, 0x60, 0x46, 0xb9, 0x35, 0x8b}};
static const GUID kIID_Dev8 = {0x9218e6bb, 0xf944, 0x4f7e, {0xa7, 0x5c, 0xb1, 0xb2, 0xc7, 0xb7, 0x01, 0xf3}};
static const GUID kIID_Dev10 = {0x517f8718, 0xaa66, 0x49f9, {0xb0, 0x2b, 0xa7, 0xab, 0x89, 0xc0, 0x60, 0x31}};
static const GUID kIID_Lib  = {0xc64226a8, 0x9201, 0x46af, {0xb4, 0xcc, 0x53, 0xfb, 0x9f, 0xf7, 0x41, 0x4f}};
static const GUID kIID_Lib1 = {0x80eabf42, 0x2568, 0x4e5e, {0xbd, 0x82, 0xc3, 0x7f, 0x86, 0x96, 0x1d, 0xc3}};

typedef struct { HRESULT (STDMETHODCALLTYPE *QI)(void*, const GUID*, void**); ULONG (STDMETHODCALLTYPE *AddRef)(void*); ULONG (STDMETHODCALLTYPE *Release)(void*); } IUnkVt;
static inline void** vt(void* obj) { return *(void***)obj; }
static inline void* qi(void* obj, const GUID* iid)
{
    void* out = NULL;
    if (obj && ((IUnkVt*)vt(obj))->QI(obj, iid, &out) >= 0) return out;
    return NULL;
}
static inline void rel(void* obj) { if (obj) ((IUnkVt*)vt(obj))->Release(obj); }

static int g_mhOk;
static int mh(void* target, void* detour, void* orig, const char* what)
{
    if (!target) return 0;
    MH_STATUS s = MH_CreateHook(target, detour, (LPVOID*)orig);
    if (s != MH_OK) {
        g_sdk->logger->WarnF(g_handle, "D3D12 hook %s not created (MinHook %d)", what, (int)s);
        return 0;
    }
    return 1;
}

static void hook_d3d12(void)
{
    typedef HRESULT (WINAPI *CreateFactory1_t)(const GUID*, void**);
    typedef HRESULT (WINAPI *GetInterface_t)(const GUID*, const GUID*, void**);
    typedef HRESULT (WINAPI *EnumWarp_t)(void*, const GUID*, void**);
    typedef HRESULT (WINAPI *SetFlags_t)(void*, UINT);
    typedef HRESULT (WINAPI *FactCreateDevice_t)(void*, void*, int, const GUID*, void**);

    /* How the hook targets are found, without touching the game's own device:
     *  - the real dxgi.dll from System32 (ReShade is the dxgi.dll in the game folder and wraps adapters),
     *  - a throwaway WARP (CPU) device made through ID3D12DeviceFactory, flagged so it never becomes
     *    the process-wide device. ReShade does not wrap the device factory, so no ReShade/RenoDX
     *    add-on events fire. The device's methods are the D3D12Core.dll runtime functions that every
     *    D3D12 device in the process uses, including the game's. */
    wchar_t sys[MAX_PATH + 16];
    UINT n = GetSystemDirectoryW(sys, MAX_PATH);
    if (!n || n >= MAX_PATH) return;
    wcscat(sys, L"\\dxgi.dll");
    HMODULE dxgi = getenv("HITCHREC_TEST") ? GetModuleHandleA("dxgi.dll") : GetModuleHandleW(sys);   /* test rig only */
    if (!dxgi) dxgi = LoadLibraryW(sys);
    HMODULE d3d12 = GetModuleHandleA("d3d12.dll");
    if (!dxgi || !d3d12) { g_sdk->logger->Warn(g_handle, "D3D12 not loaded, GPU recording off"); return; }
    CreateFactory1_t cf = (CreateFactory1_t)(void*)GetProcAddress(dxgi, "CreateDXGIFactory1");
    GetInterface_t gi = (GetInterface_t)(void*)GetProcAddress(d3d12, "D3D12GetInterface");
    if (!cf || !gi) { g_sdk->logger->Warn(g_handle, "D3D12GetInterface not available, GPU recording off"); return; }

    void* fac = NULL;
    HRESULT hr = cf(&kIID_IDXGIFactory4, &fac);
    if (FAILED(hr) || !fac) { g_sdk->logger->WarnF(g_handle, "CreateDXGIFactory1 failed 0x%08lx, GPU recording off", (unsigned long)hr); return; }
    void* warp = NULL;
    hr = ((EnumWarp_t)vt(fac)[27])(fac, &kIID_IDXGIAdapter1, &warp);
    rel(fac);
    if (FAILED(hr) || !warp) { g_sdk->logger->WarnF(g_handle, "no WARP adapter (0x%08lx), GPU recording off", (unsigned long)hr); return; }

    void* df = NULL;
    hr = gi(&kCLSID_DeviceFactory, &kIID_DeviceFactory, &df);
    if (FAILED(hr) || !df) {
        rel(warp);
        g_sdk->logger->WarnF(g_handle, "D3D12 device factory not available (0x%08lx), GPU recording off", (unsigned long)hr);
        return;
    }
    ((SetFlags_t)vt(df)[5])(df, 4 /* D3D12_DEVICE_FACTORY_FLAG_DISALLOW_STORING_NEW_DEVICE_AS_SINGLETON */);
    void* dev = NULL;
    uint64_t c0 = qpc();
    hr = ((FactCreateDevice_t)vt(df)[9])(df, warp, 0xb000 /* FL 11_0 */, &kIID_Dev, &dev);
    uint64_t c1 = qpc();
    rel(df);
    rel(warp);
    if (FAILED(hr) || !dev) { g_sdk->logger->WarnF(g_handle, "WARP device not created (0x%08lx), GPU recording off", (unsigned long)hr); return; }

    void** v0 = vt(dev);
    char modname[MAX_PATH] = "?";
    HMODULE owner = NULL;
    if (GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                           (LPCSTR)v0[10], &owner))
        GetModuleFileNameA(owner, modname, sizeof(modname));
    g_sdk->logger->InfoF(g_handle, "helper WARP device made in %.1f ms; device methods live in %s",
                         to_us(c1 - c0) / 1000.0, modname);
    /* same addresses other plugins print (e.g. ReFRACTNative "CreateGraphicsPipelineState: attached at ...")
       = proof that these are the functions the game's device uses */
    g_sdk->logger->InfoF(g_handle, "CreateGraphicsPipelineState at %p, CreateComputePipelineState at %p, CreatePlacedResource at %p",
                         v0[10], v0[11], v0[29]);
    const char* base = strrchr(modname, '\\');
    base = base ? base + 1 : modname;
    if (_stricmp(base, "D3D12Core.dll") != 0 && !getenv("HITCHREC_TEST")) {
        g_sdk->logger->Warn(g_handle, "device methods are not in D3D12Core.dll, GPU recording off");
        rel(dev);
        return;
    }

    uint32_t ok = 0;
    if (g_cfg.gpuPso) {
        if (mh(v0[10], (void*)hkGfx, &oGfx4, "CreateGraphicsPipelineState") |
            mh(v0[11], (void*)hkComp, &oComp4, "CreateComputePipelineState"))
            ok |= HOOK_D3D_PSO;
        void* d2 = qi(dev, &kIID_Dev2);
        if (d2) { if (mh(vt(d2)[47], (void*)hkStream, &oStream4, "CreatePipelineState")) ok |= HOOK_D3D_PSO; rel(d2); }
        void* d5 = qi(dev, &kIID_Dev5);
        if (d5) { if (mh(vt(d5)[62], (void*)hkStateObj, &oStateObj4, "CreateStateObject")) ok |= HOOK_D3D_RT; rel(d5); }
        void* d7 = qi(dev, &kIID_Dev7);
        if (d7) { if (mh(vt(d7)[66], (void*)hkAddState, &oAddState5, "AddToStateObject")) ok |= HOOK_D3D_RT; rel(d7); }

        /* pipeline library: make an empty one only to find its methods, then release it */
        void* d1 = qi(dev, &kIID_Dev1);
        if (d1) {
            typedef HRESULT (WINAPI *CreateLib_t)(void*, const void*, SIZE_T, const GUID*, void**);
            void* lib = NULL;
            if (SUCCEEDED(((CreateLib_t)vt(d1)[44])(d1, NULL, 0, &kIID_Lib, &lib)) && lib) {
                if (mh(vt(lib)[9], (void*)hkLibGfx, &oLibGfx5, "LoadGraphicsPipeline") |
                    mh(vt(lib)[10], (void*)hkLibComp, &oLibComp5, "LoadComputePipeline"))
                    ok |= HOOK_D3D_LIB;
                void* lib1 = qi(lib, &kIID_Lib1);
                if (lib1) { if (mh(vt(lib1)[13], (void*)hkLib1Load, &oLib1Load5, "LoadPipeline")) ok |= HOOK_D3D_LIB; rel(lib1); }
                rel(lib);
            }
            rel(d1);
        }
    }
    if (g_cfg.gpuRes) {
        if (mh(v0[27], (void*)hkCommit, &oCommit8, "CreateCommittedResource") |
            mh(v0[28], (void*)hkHeap, &oHeap4, "CreateHeap") |
            mh(v0[29], (void*)hkPlaced, &oPlaced8, "CreatePlacedResource"))
            ok |= HOOK_D3D_RES;
        void* d4 = qi(dev, &kIID_Dev4);
        if (d4) { mh(vt(d4)[53], (void*)hkCommit1, &oCommit1_9, "CreateCommittedResource1");
                  mh(vt(d4)[54], (void*)hkHeap1, &oHeap1_5, "CreateHeap1"); rel(d4); }
        void* d8 = qi(dev, &kIID_Dev8);
        if (d8) { mh(vt(d8)[69], (void*)hkCommit2, &oCommit2_9, "CreateCommittedResource2");
                  mh(vt(d8)[70], (void*)hkPlaced1, &oPlaced1_8, "CreatePlacedResource1"); rel(d8); }
        void* d10 = qi(dev, &kIID_Dev10);
        if (d10) { mh(vt(d10)[76], (void*)hkCommit3, &oCommit3_11, "CreateCommittedResource3");
                   mh(vt(d10)[77], (void*)hkPlaced2, &oPlaced2_10, "CreatePlacedResource2"); rel(d10); }
    }
    rel(dev);

    MH_STATUS s = MH_EnableHook(MH_ALL_HOOKS);
    if (s != MH_OK) {
        g_sdk->logger->WarnF(g_handle, "MinHook enable failed (%d), GPU recording off", (int)s);
        return;
    }
    g_hooks |= ok;
    g_sdk->logger->InfoF(g_handle, "D3D12 hooks on (pso=%d rt=%d lib=%d res=%d)",
                         !!(ok & HOOK_D3D_PSO), !!(ok & HOOK_D3D_RT), !!(ok & HOOK_D3D_LIB), !!(ok & HOOK_D3D_RES));
}

/* ------------------------------------------------------------------ */
/* Writer thread: drains the buffers, writes .bin, builds the .log      */
/* ------------------------------------------------------------------ */
#define RING (1u << 16)
static Rec g_ring[RING];               /* last non-frame records, for spike look-back */
static uint32_t g_ringPos, g_ringFill;

typedef struct { uint32_t n; uint64_t sum, max, maxHash; } Tally;
typedef struct {
    uint64_t s, e;                     /* spike window, us */
    uint32_t frame;
    Tally t[K_MAX][2];                 /* [kind][0 other threads, 1 main thread] */
} Spike;
#define MAXSPIKE 64
static Spike g_spk[MAXSPIKE];
static int g_nspk;

/* per-summary-period stats */
static float* g_ft; static uint32_t g_ftN, g_ftCap;
static uint64_t g_perStart;
static Tally g_per[K_MAX];
static uint32_t g_perIdx;
static uint64_t g_bytes; static int g_capHit;
static uint64_t g_totalFrames, g_totalSpikes;

static void tally(Tally* t, const Rec* r)
{
    t->n++; t->sum += r->dur_us;
    if (r->dur_us >= t->max) { t->max = r->dur_us; t->maxHash = r->hash; }
}

static void spike_add(const Rec* r)
{
    uint64_t a = r->t_us, b = r->t_us + r->dur_us;
    for (int i = 0; i < g_nspk; ++i) {
        Spike* s = &g_spk[i];
        if (a < s->e && (b > s->s || (r->dur_us == 0 && a >= s->s)))
            tally(&s->t[r->kind][r->flags & 1], r);
    }
}

static void fmt_time(char* out, size_t n, uint64_t us)
{
    uint64_t s = us / 1000000;
    snprintf(out, n, "%02llu:%02llu:%02llu.%03llu", (unsigned long long)(s / 3600), (unsigned long long)(s / 60 % 60),
             (unsigned long long)(s % 60), (unsigned long long)(us / 1000 % 1000));
}

static void spike_write(const Spike* s)
{
    if (!g_log) return;
    char ts[32]; fmt_time(ts, sizeof ts, s->s);
    fprintf(g_log, "SPIKE %s frame %u  %.1f ms", ts, s->frame, (s->e - s->s) / 1000.0);
    for (int side = 1; side >= 0; --side) {
        fprintf(g_log, side ? "  | main thread:" : "  | other threads:");
        int any = 0;
        for (int k = 1; k < K_MAX; ++k) {
            const Tally* t = &s->t[k][side];
            if (!t->n || !g_kindName[k]) continue;
            any = 1;
            fprintf(g_log, " %s x%u sum %.1f max %.1f", g_kindName[k], t->n, t->sum / 1000.0, t->max / 1000.0);
            if (t->maxHash && (k < K_PSO_GFX || k == K_PSO_GFX || k == K_PSO_COMP))
                fprintf(g_log, " [%016llx]", (unsigned long long)t->maxHash);
            fprintf(g_log, ";");
        }
        if (!any) fprintf(g_log, " -");
    }
    fprintf(g_log, "\n");
}

static void spikes_flush(uint64_t now_us, int all)
{
    int j = 0;
    for (int i = 0; i < g_nspk; ++i) {
        if (all || now_us > g_spk[i].e + 1500000) spike_write(&g_spk[i]);
        else g_spk[j++] = g_spk[i];
    }
    g_nspk = j;
}

static int cmpf(const void* a, const void* b) { float x = *(const float*)a, y = *(const float*)b; return (x > y) - (x < y); }

static void summary_write(uint64_t end_us)
{
    if (!g_log || (!g_ftN && !g_per[K_LOAD].n)) { g_perStart = end_us; return; }
    char ts[32]; fmt_time(ts, sizeof ts, g_perStart);
    double secs = (end_us - g_perStart) / 1e6;
    double sum = 0; uint32_t o33 = 0, o50 = 0, o100 = 0;
    for (uint32_t i = 0; i < g_ftN; ++i) {
        sum += g_ft[i];
        if (g_ft[i] > 33.4f) o33++;
        if (g_ft[i] > 50.f) o50++;
        if (g_ft[i] > 100.f) o100++;
    }
    float med = 0, p99 = 0, p999 = 0, mx = 0;
    if (g_ftN) {
        qsort(g_ft, g_ftN, sizeof(float), cmpf);
        med = g_ft[g_ftN / 2];
        p99 = g_ft[(uint32_t)((g_ftN - 1) * 0.99)];
        p999 = g_ft[(uint32_t)((g_ftN - 1) * 0.999)];
        mx = g_ft[g_ftN - 1];
    }
    fprintf(g_log, "\nPERIOD %u  start %s  %.0f s  frames %u  avg %.1f fps  median %.2f ms  99%% %.2f ms  99.9%% %.2f ms  worst %.1f ms  >33ms %u  >50ms %u  >100ms %u\n",
            ++g_perIdx, ts, secs, g_ftN, sum > 0 ? g_ftN / (sum / 1000.0) : 0.0, med, p99, p999, mx, o33, o50, o100);
    fprintf(g_log, "   ");
    for (int k = 1; k < K_MAX; ++k) {
        if (!g_per[k].n || !g_kindName[k]) continue;
        fprintf(g_log, " %s %u (avg %.2f, max %.1f ms);", g_kindName[k], g_per[k].n,
                g_per[k].sum / 1000.0 / g_per[k].n, g_per[k].max / 1000.0);
    }
    if (g_dropped) fprintf(g_log, " DROPPED %lld", (long long)g_dropped);
    fprintf(g_log, "\n\n");
    memset(g_per, 0, sizeof g_per);
    g_ftN = 0;
    g_perStart = end_us;
}

static void process(const Rec* r, uint32_t n)
{
    for (uint32_t i = 0; i < n; ++i) {
        const Rec* x = &r[i];
        if (x->kind == K_FRAME) {
            g_totalFrames++;
            if (!g_perStart) g_perStart = x->t_us;
            if (g_ftN == g_ftCap) {
                uint32_t nc = g_ftCap ? g_ftCap * 2 : 65536;
                float* p = (float*)realloc(g_ft, nc * sizeof(float));
                if (p) { g_ft = p; g_ftCap = nc; }
            }
            if (g_ftN < g_ftCap) g_ft[g_ftN++] = x->dur_us / 1000.0f;
            if (x->dur_us >= (uint32_t)g_cfg.spikeMs * 1000u && x->frame > 30) {
                g_totalSpikes++;
                if (g_shared) InterlockedIncrement(&g_shared->spikes);
                if (g_nspk == MAXSPIKE) { spike_write(&g_spk[0]); memmove(g_spk, g_spk + 1, sizeof(Spike) * (MAXSPIKE - 1)); g_nspk--; }
                Spike* s = &g_spk[g_nspk++];
                memset(s, 0, sizeof *s);
                s->s = x->t_us; s->e = x->t_us + x->dur_us; s->frame = x->frame;
                for (uint32_t j = 0; j < g_ringFill; ++j) {      /* look back at work that already finished */
                    const Rec* y = &g_ring[j];
                    uint64_t a = y->t_us, b = y->t_us + y->dur_us;
                    if (a < s->e && (b > s->s || (y->dur_us == 0 && a >= s->s))) tally(&s->t[y->kind][y->flags & 1], y);
                }
            }
            if (x->t_us + x->dur_us >= g_perStart + (uint64_t)g_cfg.summarySec * 1000000ull)
                summary_write(x->t_us + x->dur_us);
        } else if (x->kind == K_MARK) {
            if (g_log && x->extra >= 2 && x->extra <= 5) {
                char ts[32]; fmt_time(ts, sizeof ts, x->t_us);
                if (x->extra == 3) fprintf(g_log, "MARK  %s  frame %u  (you pressed the mark key)\n", ts, x->frame);
                else if (x->extra == 2) fprintf(g_log, "D3D12 hooks set at %s  frame %u  (hooks 0x%llx)\n", ts, x->frame, (unsigned long long)x->hash);
                else if (x->extra == 4) fprintf(g_log, "\n==== CAPTURE START  %s  frame %u ====\n", ts, x->frame);
                else fprintf(g_log, "==== CAPTURE STOP   %s  frame %u ====\n\n", ts, x->frame);
            }
            if (x->extra == 4) { g_perStart = 0; g_ftN = 0; memset(g_per, 0, sizeof g_per); }
            if (x->extra == 5 && g_ftN) summary_write(x->t_us);
        } else if (x->kind == K_VRAM) {
            /* not work: kept out of spike tallies */
        } else if (x->kind < K_MAX) {
            tally(&g_per[x->kind], x);
            spike_add(x);
            g_ring[g_ringPos] = *x;
            g_ringPos = (g_ringPos + 1) & (RING - 1);
            if (g_ringFill < RING) g_ringFill++;
        }
    }
}

static void drain(void)
{
    AcquireSRWLockExclusive(&g_lock);
    int a = g_active;
    g_active ^= 1;
    g_cnt[g_active] = 0;
    uint32_t n = g_cnt[a];
    ReleaseSRWLockExclusive(&g_lock);
    if (!n) return;

    if (g_bin && !g_capHit) {
        if (g_bytes + (uint64_t)n * sizeof(Rec) > (uint64_t)g_cfg.maxMB << 20) {
            g_capHit = 1;
            if (g_log) fprintf(g_log, "NOTE: MaxMB reached, .bin recording stopped (log continues)\n");
        } else {
            fwrite(g_buf[a], sizeof(Rec), n, g_bin);
            g_bytes += (uint64_t)n * sizeof(Rec);
        }
    }
    process(g_buf[a], n);
}

/* VRAM: the game's own use against its budget (QueryVideoMemoryInfo answers for the calling process,
   and this runs inside the game). Uses the System32 dxgi, so no ReShade/RenoDX wrapper is involved. */
static void* g_adapter3;
static int g_vramState;               /* 0 not tried, 1 ok, -1 unavailable */
static void vram_init(void)
{
    static const GUID iidF1 = {0x770aae78, 0xf26f, 0x4dba, {0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}};
    static const GUID iidA3 = {0x645967a4, 0x1392, 0x4310, {0xa7, 0x98, 0x80, 0x53, 0xce, 0x3e, 0x93, 0xfd}};
    typedef HRESULT (WINAPI *CF1)(const GUID*, void**);
    typedef HRESULT (WINAPI *Enum1)(void*, UINT, void**);
    typedef HRESULT (WINAPI *Desc1)(void*, void*);
    g_vramState = -1;
    wchar_t sys[MAX_PATH + 16];
    UINT n = GetSystemDirectoryW(sys, MAX_PATH);
    if (!n || n >= MAX_PATH) return;
    wcscat(sys, L"\\dxgi.dll");
    HMODULE dxgi = getenv("HITCHREC_TEST") ? GetModuleHandleA("dxgi.dll") : GetModuleHandleW(sys);
    if (!dxgi) return;
    CF1 cf = (CF1)(void*)GetProcAddress(dxgi, "CreateDXGIFactory1");
    void* fac = NULL;
    if (!cf || FAILED(cf(&iidF1, &fac)) || !fac) return;
    void* best = NULL; uint64_t bestMem = 0;
    for (UINT i = 0; i < 16; ++i) {
        void* a = NULL;
        if (FAILED(((Enum1)vt(fac)[12])(fac, i, &a)) || !a) break;
        uint8_t d[312]; memset(d, 0, sizeof d);
        if (SUCCEEDED(((Desc1)vt(a)[10])(a, d))) {
            uint64_t ded = *(uint64_t*)(d + 272);
            uint32_t flags = *(uint32_t*)(d + 304);
            if (!(flags & 2) && ded > bestMem) { if (best) rel(best); best = a; bestMem = ded; continue; }
        }
        rel(a);
    }
    rel(fac);
    if (!best) return;
    g_adapter3 = qi(best, &iidA3);
    rel(best);
    if (g_adapter3) g_vramState = 1;
}
static void vram_sample(void)
{
    typedef HRESULT (WINAPI *QVMI)(void*, UINT, int, void*);
    if (g_vramState == 0) vram_init();
    if (g_vramState != 1) return;
    uint64_t loc[4] = {0}, non[4] = {0};
    if (FAILED(((QVMI)vt(g_adapter3)[14])(g_adapter3, 0, 0, loc))) return;   /* DXGI_MEMORY_SEGMENT_GROUP_LOCAL */
    ((QVMI)vt(g_adapter3)[14])(g_adapter3, 0, 1, non);                       /* NON_LOCAL (shared system memory) */
    if (g_shared) { g_shared->vramUsage = loc[1]; g_shared->vramBudget = loc[0]; g_shared->sharedUsage = non[1]; }
    uint64_t t = qpc();
    uint64_t budMB = loc[0] >> 20, nonMB = non[1] >> 20;
    Rec r; memset(&r, 0, sizeof r);
    r.t_us = to_us(t - (uint64_t)g_qpc0.QuadPart);
    r.hash = loc[1];
    r.frame = (uint32_t)g_frame;
    r.dur_us = (uint32_t)(budMB > 0xFFFFFFFFull ? 0xFFFFFFFFu : budMB);
    r.kind = K_VRAM;
    r.extra = (uint16_t)(nonMB > 0xFFFF ? 0xFFFF : nonMB);
    r.tid = GetCurrentThreadId();
    if (!g_capturing) return;
    buf_put(&r);
}

static DWORD WINAPI writer_main(void* unused)
{
    (void)unused;
    for (;;) {
        HANDLE hs[2] = { g_stop, g_wake };
        DWORD w = WaitForMultipleObjects(g_wake ? 2 : 1, hs, FALSE, 200);
        if (g_cfg.vram && g_frame >= 150) vram_sample();   /* start once the game is drawing (menu) */
        script_flush(0);
        drain();
        spikes_flush(to_us(qpc() - (uint64_t)g_qpc0.QuadPart), 0);
        if (g_bin) fflush(g_bin);
        if (g_log) fflush(g_log);
        if (w == WAIT_OBJECT_0) break;
    }
    script_flush(1);
    if (g_cfg.scripts) owners_write();
    drain();
    spikes_flush(0, 1);
    summary_write(to_us(qpc() - (uint64_t)g_qpc0.QuadPart));
    if (g_log) {
        fprintf(g_log, "END  frames %llu  spikes >= %d ms: %llu  records dropped: %lld  bin %.1f MB\n",
                (unsigned long long)g_totalFrames, g_cfg.spikeMs, (unsigned long long)g_totalSpikes,
                (long long)g_dropped, g_bytes / 1048576.0);
    }
    return 0;
}

/* ------------------------------------------------------------------ */
/* Game state callbacks                                                */
/* ------------------------------------------------------------------ */
static bool running_enter(void* app)
{
    (void)app;
    g_mainTid = GetCurrentThreadId();
    if (g_cetMissing && g_mhOk && g_cfg.cet && g_tls != TLS_OUT_OF_INDEXES) hook_cet();   /* CET loaded after us: mods it already started stay "CET itself" until reloaded */
    uint64_t t = qpc();
    push(K_MARK, g_hooks, t, t, 1, 0);
    if (g_log) {
        fprintf(g_log, "Running state entered (main thread %lu). Engine hooks: 0x%x\n", (unsigned long)g_mainTid, (unsigned)g_hooks);
        if (g_hooks & HOOK_SCRIPT_EXEC)
            fprintf(g_log, "Script functions bound to source files: %ld, mods with scripts: %ld (timed while %s)\n",
                    (long)g_bound, (long)g_boundMods, g_cfg.captureMode == 2 ? "an F11 mark is on" : g_cfg.captureMode == 1 ? "recording" : "the game runs");
        if (g_hooks & HOOK_CET)
            fprintf(g_log, "CET Lua timing on, CET mods recognised so far: %ld\n", (long)(g_cetOwners - 1));
        else if (g_cfg.cet)
            fprintf(g_log, "CET Lua timing off (CET not loaded or not recognised)\n");
        fflush(g_log);
    }
    return true;   /* one-shot */
}

static int g_d3dDone, g_keyDown, g_capKeyDown;
static LONG g_focus;   /* session mode: F11 part marked */
/* key state; the offline test rig feeds keys through a small shared map instead of the keyboard */
static const volatile uint8_t* g_testKeys;
static int key_down(int vk)
{
    if (!g_testKeys && getenv("HITCHREC_TEST")) {
        HANDLE m = OpenFileMappingW(FILE_MAP_READ, FALSE, L"Local\\HitchRecorder.TestKeys");
        if (m) g_testKeys = (const volatile uint8_t*)MapViewOfFile(m, FILE_MAP_READ, 0, 0, 256);
    }
    if (g_testKeys) return g_testKeys[vk & 0xFF] != 0;
    return (GetAsyncKeyState(vk) & 0x8000) != 0;
}
static bool running_update(void* app)
{
    (void)app;
    uint64_t t = qpc();
    if (g_lastTick) {
        LONG f = InterlockedIncrement(&g_frame);
        push(K_FRAME, 0, g_lastTick, t, 0, 0);
        /* D3D12 hooks only once frames are being drawn, so the game's device certainly exists */
        if (!g_d3dDone && f >= 120) {
            g_d3dDone = 1;
            if (g_cfg.gpuPso || g_cfg.gpuRes) {
                hook_d3d12();
                uint64_t t2 = qpc();
                push(K_MARK, g_hooks, t2, t2, 2, 0);
            }
        }
        if (g_cfg.markKey) {
            int down = key_down(g_cfg.markKey);
            if (down && !g_keyDown) { push(K_MARK, 0, t, t, 3, 0); if (g_shared) InterlockedIncrement(&g_shared->markers); }
            g_keyDown = down;
        }
        if (g_cfg.captureMode == 2 && g_cfg.captureKey) {
            /* session mode (Night City Frame Lab): everything is recorded; F11 marks the part to look at */
            int down = key_down(g_cfg.captureKey);
            if (down && !g_capKeyDown) {
                g_focus = !g_focus;
                script_set(g_focus);
                push(K_MARK, 0, t, t, g_focus ? 6 : 7, 0);
                if (g_shared) {
                    if (g_focus) { g_shared->captureStartQpc = t; g_shared->captureStopQpc = 0; }
                    else g_shared->captureStopQpc = t;
                    g_shared->capturing = g_focus;
                }
            }
            g_capKeyDown = down;
        }
        if (g_cfg.captureMode == 1 && g_cfg.captureKey) {
            int down = key_down(g_cfg.captureKey);
            if (down && !g_capKeyDown) {
                if (!g_capturing) {
                    g_capturing = 1;
                    script_set(1);
                    push(K_MARK, 0, t, t, 4, 0);
                    if (g_shared) { g_shared->spikes = 0; g_shared->markers = 0; g_shared->captureStartQpc = t; g_shared->captureStopQpc = 0; }
                } else {
                    push(K_MARK, 0, t, t, 5, 0);
                    g_capturing = 0;
                    script_set(0);
                    if (g_shared) g_shared->captureStopQpc = t;
                }
                if (g_shared) g_shared->capturing = g_capturing;
            }
            g_capKeyDown = down;
        }
        if (g_shared) {
            float ms = (float)(to_us(t - g_lastTick) / 1000.0);
            g_shared->frame = f;
            g_shared->lastFrameQpc = t;
            g_shared->lastFrameMs = ms;
            g_shared->avgFrameMs = g_shared->avgFrameMs <= 0 ? ms : g_shared->avgFrameMs * 0.98f + ms * 0.02f;
            g_shared->hooks = g_hooks;
        }
    }
    g_lastTick = qpc();   /* one-off hooking time at frame 120 is left out of the frame times */
    return false;  /* false = keep calling every frame */
}

static bool running_exit(void* app) { (void)app; return true; }
static R4GameState g_running = { running_enter, running_update, running_exit };

/* ------------------------------------------------------------------ */
/* Setup                                                               */
/* ------------------------------------------------------------------ */
static void read_cfg(void)
{
    wchar_t ini[MAX_PATH];
    _snwprintf(ini, MAX_PATH, L"%lsHitchRecorder.ini", g_dir);
    g_cfg.enabled   = GetPrivateProfileIntW(L"Recorder", L"Enabled", 1, ini);
    g_cfg.loads     = GetPrivateProfileIntW(L"Recorder", L"RecordLoads", 1, ini);
    g_cfg.entities  = GetPrivateProfileIntW(L"Recorder", L"RecordEntities", 1, ini);
    g_cfg.gpuPso    = GetPrivateProfileIntW(L"Recorder", L"RecordShaders", 1, ini);
    g_cfg.gpuRes    = GetPrivateProfileIntW(L"Recorder", L"RecordGpuMemory", 1, ini);
    g_cfg.spikeMs   = GetPrivateProfileIntW(L"Recorder", L"SpikeMs", 50, ini);
    g_cfg.maxMB     = GetPrivateProfileIntW(L"Recorder", L"MaxMB", 2048, ini);
    g_cfg.summarySec= GetPrivateProfileIntW(L"Recorder", L"SummarySeconds", 60, ini);
    g_cfg.markKey   = GetPrivateProfileIntW(L"Recorder", L"MarkKey", 0x91, ini);   /* 0x91 = Scroll Lock, 0 = off */
    g_cfg.captureMode = GetPrivateProfileIntW(L"Recorder", L"CaptureMode", 1, ini); /* 0 always, 1 capture key */
    g_cfg.captureKey  = GetPrivateProfileIntW(L"Recorder", L"CaptureKey", 0x7A, ini); /* 0x7A = F11 */
    g_cfg.vram        = GetPrivateProfileIntW(L"Recorder", L"RecordVram", 1, ini);
    g_cfg.sessionOnly = GetPrivateProfileIntW(L"Recorder", L"SessionOnly", 0, ini);   /* 1 = switch itself off for the next start */
    g_cfg.scripts     = GetPrivateProfileIntW(L"Recorder", L"RecordScripts", 1, ini);  /* REDscript time per mod (F11 parts only in session mode) */
    g_cfg.cet         = GetPrivateProfileIntW(L"Recorder", L"RecordCET", 1, ini);      /* CET Lua time per mod (same) */
    if (g_cfg.captureMode < 0 || g_cfg.captureMode > 2) g_cfg.captureMode = 1;     /* 0 always, 1 F11 on/off, 2 session (F11 marks) */
    if (g_cfg.spikeMs < 5) g_cfg.spikeMs = 5;
    if (g_cfg.summarySec < 10) g_cfg.summarySec = 10;
    if (g_cfg.maxMB < 16) g_cfg.maxMB = 16;
}

static void attach(uint32_t hash, void* detour, void** orig, uint32_t bit, const char* what)
{
    typedef uintptr_t (*Resolve_t)(uint32_t);
    static Resolve_t resolve;
    if (!resolve) {
        HMODULE r = GetModuleHandleW(L"RED4ext.dll");
        if (r) resolve = (Resolve_t)(void*)GetProcAddress(r, "RED4ext_ResolveAddress");
        if (!resolve) { g_sdk->logger->Warn(g_handle, "RED4ext_ResolveAddress not found"); return; }
    }
    uintptr_t addr = resolve(hash);
    if (!addr) { g_sdk->logger->WarnF(g_handle, "%s: address %u not known for this game version, skipped", what, hash); return; }
    if (g_sdk->hooking->Attach(g_handle, (void*)addr, detour, orig)) {
        g_hooks |= bit;
    } else {
        g_sdk->logger->WarnF(g_handle, "%s: hook not attached", what);
    }
}

static void open_outputs(void)
{
    wchar_t dir[MAX_PATH], path[MAX_PATH];
    _snwprintf(dir, MAX_PATH, L"%lsrecordings", g_dir);
    CreateDirectoryW(dir, NULL);
    SYSTEMTIME st; GetLocalTime(&st);
    _snwprintf(path, MAX_PATH, L"%ls\\HitchRecorder_%04u-%02u-%02u_%02u-%02u-%02u.bin", dir,
               st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond);
    g_bin = _wfopen(path, L"wb");
    wcsncpy(g_binPath, path, MAX_PATH - 1);
    if (g_bin) setvbuf(g_bin, NULL, _IOFBF, 1 << 20);
    size_t L = wcslen(path);
    wcscpy(path + L - 3, L"log");
    g_log = _wfopen(path, L"w");
    if (!g_bin || !g_log) g_sdk->logger->Warn(g_handle, "could not create files in recordings folder");
}

static void write_header(void)
{
    if (!g_bin) return;
    Hdr h; memset(&h, 0, sizeof h);
    memcpy(h.magic, "HREC", 4);
    h.version = 1;
    h.qpc_freq = (uint64_t)g_freq.QuadPart;
    h.qpc0 = (uint64_t)g_qpc0.QuadPart;
    FILETIME ft; GetSystemTimeAsFileTime(&ft);
    h.filetime0 = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime;
    if (g_sdk->runtime) { h.game_major = g_sdk->runtime->major; h.game_minor = g_sdk->runtime->minor; h.game_patch = g_sdk->runtime->patch; }
    h.hooks = g_hooks;
    h.rec_size = sizeof(Rec);
    h.spike_ms = (uint32_t)g_cfg.spikeMs;
    h.plugin_version = HR_VERSION_MAJOR * 10000 + HR_VERSION_MINOR * 100 + HR_VERSION_PATCH;
    h.capture_mode = (uint8_t)g_cfg.captureMode;
    fwrite(&h, sizeof h, 1, g_bin);
}

static void shared_open(void)
{
    g_sharedMap = CreateFileMappingW(INVALID_HANDLE_VALUE, NULL, PAGE_READWRITE, 0, sizeof(SharedState), L"Local\\HitchRecorder.State");
    if (!g_sharedMap) return;
    g_shared = (SharedState*)MapViewOfFile(g_sharedMap, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(SharedState));
    if (!g_shared) return;
    memset(g_shared, 0, sizeof *g_shared);
    g_shared->version = 1;
    g_shared->pid = GetCurrentProcessId();
    g_shared->pluginVersion = HR_VERSION_MAJOR * 10000 + HR_VERSION_MINOR * 100 + HR_VERSION_PATCH;
    g_shared->qpcFreq = (uint64_t)g_freq.QuadPart;
    g_shared->qpc0 = (uint64_t)g_qpc0.QuadPart;
    FILETIME ft; GetSystemTimeAsFileTime(&ft);
    g_shared->filetime0 = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime;
    g_shared->captureMode = (uint32_t)g_cfg.captureMode;
    g_shared->capturing = g_cfg.captureMode == 2 ? g_focus : g_capturing;   /* session mode: F11 part marked */
    if (g_binPath[0]) wcsncpy(g_shared->binPath, g_binPath, 259);
    MemoryBarrier();
    g_shared->magic = 0x54535248;   /* "HRST" */
}

static bool on_load(void)
{
    QueryPerformanceFrequency(&g_freq);
    QueryPerformanceCounter(&g_qpc0);

    HMODULE self = NULL;
    GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                       (LPCWSTR)(void*)on_load, &self);
    GetModuleFileNameW(self, g_dir, MAX_PATH);
    wchar_t* slash = wcsrchr(g_dir, L'\\');
    if (slash) slash[1] = 0;

    read_cfg();
    if (!g_cfg.enabled) { g_sdk->logger->Info(g_handle, "disabled in HitchRecorder.ini"); return true; }
    if (g_cfg.sessionOnly) {
        /* one session only: switched off in the ini right away, so the next game start is clean
           even if Night City Frame Lab never gets to take it out (game or PC crash) */
        wchar_t ini[MAX_PATH];
        _snwprintf(ini, MAX_PATH, L"%lsHitchRecorder.ini", g_dir);
        if (WritePrivateProfileStringW(L"Recorder", L"Enabled", L"0", ini))
            g_sdk->logger->Info(g_handle, "one session: switched itself off for the next game start");
        else
            g_sdk->logger->Warn(g_handle, "one session: could not switch itself off in the ini");
    }

    R4SemVer v = g_sdk->runtime ? *g_sdk->runtime : (R4SemVer){0};
    if (g_cfg.loads) {
        attach(H_LOADREQ, (void*)hkLoadReq, (void**)&oLoadReq, HOOK_LOADREQ, "IssueLoadingRequest");
        uint32_t upd = 0;
        /* only on 2.31: hash 3955625134 (2.5's OnUpdate) is a different function in 2.31,
           so on any other version this one hook stays off until checked.
           RED4ext reports game 2.31 as 2.3.1 (0.2.1 only accepted 2.31 and never hooked it). */
        if (v.major == 2 && (v.minor == 31 || (v.minor == 3 && v.patch == 1))) upd = H_LOADERUPD_231;
        if (upd) attach(upd, (void*)hkLoaderUpd, (void**)&oLoaderUpd, HOOK_LOADERUPD, "ResourceLoader::OnUpdate");
        else g_sdk->logger->WarnF(g_handle, "game %u.%u.%u: ResourceLoader::OnUpdate not hooked (unknown version)", v.major, v.minor, v.patch);
        attach(H_SERLOAD, (void*)hkSerLoad, (void**)&oSerLoad, HOOK_SERLOAD, "ResourceSerializer::Load");
        attach(H_DESER, (void*)hkDeser, (void**)&oDeser, HOOK_DESER, "ResourceSerializer::Deserialize");
        attach(H_SECTOR, (void*)hkSector, (void**)&oSector, HOOK_SECTOR, "StreamingSector::PostLoad");
    }
    if (g_cfg.entities)
        attach(H_ENTITY, (void*)hkEntAttach, (void**)&oEntAttach, HOOK_ENTITY, "Entity::Attach");
    if (g_cfg.scripts || g_cfg.cet) {
        g_tls = TlsAlloc();
        g_sbTicks = (uint64_t)g_freq.QuadPart / 20;      /* 50 ms */
    }
    if (g_cfg.scripts) {
        /* the interpreter entry was checked against the 2.31 exe only */
        if (v.major == 2 && (v.minor == 31 || (v.minor == 3 && v.patch == 1))) {
            strcpy(g_ownerName[0], "game scripts");
            strcpy(g_ownerName[1], "scripts without a source file");
            if (g_tls != TLS_OUT_OF_INDEXES) {
                attach(H_BINDFUNC, (void*)hkBind, (void**)&oBind, HOOK_SCRIPT_BIND, "script function bind");
                if (g_hooks & HOOK_SCRIPT_BIND)
                    attach(H_SCRIPTEXEC, (void*)hkExec, (void**)&oExec, HOOK_SCRIPT_EXEC, "script function execute");
            }
        } else {
            g_sdk->logger->WarnF(g_handle, "game %u.%u.%u: REDscript timing not hooked (checked on 2.31 only)", v.major, v.minor, v.patch);
        }
    }

    if (g_sdk->gameStates->Add(g_handle, R4_STATE_RUNNING, &g_running)) g_hooks |= HOOK_FRAME;
    else g_sdk->logger->Warn(g_handle, "could not register the Running game state; no frame times");

    g_mhOk = (g_cfg.gpuPso || g_cfg.gpuRes || g_cfg.cet) && MH_Initialize() == MH_OK;
    if (!g_mhOk) g_cfg.gpuPso = g_cfg.gpuRes = 0;
    strcpy(g_cetName[0], "CET itself");
    if (g_mhOk && g_cfg.cet && g_tls != TLS_OUT_OF_INDEXES) hook_cet();

    g_capturing = g_cfg.captureMode != 1;
    script_set(g_cfg.captureMode == 0);
    open_outputs();
    write_header();
    shared_open();
    if (g_log) {
        SYSTEMTIME st; GetLocalTime(&st);
        fprintf(g_log, "HitchRecorder %d.%d.%d  game %u.%u.%u  started %04u-%02u-%02u %02u:%02u:%02u\n",
                HR_VERSION_MAJOR, HR_VERSION_MINOR, HR_VERSION_PATCH, v.major, v.minor, v.patch,
                st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond);
        fprintf(g_log, "Engine hooks at load: 0x%x   SpikeMs %d   (times below = since game start)\n",
                (unsigned)g_hooks, g_cfg.spikeMs);
        if (g_cfg.captureMode == 1)
            fprintf(g_log, "Capture mode: records only between presses of the capture key (VK %d; F11 = 122)\n\n", g_cfg.captureKey);
        else if (g_cfg.captureMode == 2)
            fprintf(g_log, "Session mode (Night City Frame Lab): records the whole game run; the capture key (VK %d) marks the part to look at\n\n", g_cfg.captureKey);
        else
            fprintf(g_log, "Capture mode: records all the time\n\n");
        fflush(g_log);
    }
    g_stop = CreateEventW(NULL, TRUE, FALSE, NULL);
    g_wake = CreateEventW(NULL, FALSE, FALSE, NULL);   /* auto-reset: one buffer swap per signal */
    g_writer = CreateThread(NULL, 0, writer_main, NULL, 0, NULL);
    if (g_writer) SetThreadPriority(g_writer, THREAD_PRIORITY_BELOW_NORMAL);

    g_sdk->logger->InfoF(g_handle, "recording (engine hooks 0x%x)", (unsigned)g_hooks);
    return true;
}

static void on_unload(void)
{
    if (g_mhOk) { MH_DisableHook(MH_ALL_HOOKS); }
    if (g_capturing && g_cfg.captureMode == 1) {          /* game closed while capturing = stop */
        uint64_t t = qpc();
        push(K_MARK, 0, t, t, 5, 0);
    }
    if (g_cfg.captureMode == 2 && g_focus) {               /* game closed inside a marked part */
        uint64_t t = qpc();
        push(K_MARK, 0, t, t, 7, 0);
    }
    if (g_shared) { g_shared->capturing = 0; g_shared->magic = 0; }
    if (g_stop) { SetEvent(g_stop); }
    if (g_writer) { WaitForSingleObject(g_writer, 3000); CloseHandle(g_writer); g_writer = NULL; }
    if (g_bin) { fclose(g_bin); g_bin = NULL; }
    if (g_log) { fclose(g_log); g_log = NULL; }
    /* MinHook is left initialised: the game is shutting down and other threads may still be inside a hook. */
}

/* ------------------------------------------------------------------ */
/* Exports                                                             */
/* ------------------------------------------------------------------ */
__declspec(dllexport) bool Main(R4Handle handle, uint8_t reason, const R4Sdk* sdk)
{
    g_handle = handle;
    g_sdk = sdk;
    if (reason == 0) return on_load();
    on_unload();
    return true;
}

__declspec(dllexport) void Query(R4PluginInfo* info)
{
    info->sdk = (R4SemVer){ 1, 0, 0, { 0, 0 } };
    info->name = L"HitchRecorder";
    info->author = L"Night City Frame Lab";
    info->version = (R4SemVer){ HR_VERSION_MAJOR, HR_VERSION_MINOR, HR_VERSION_PATCH, { 0, 0 } };
    info->runtime = (R4FileVer){ 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF };   /* version independent */
}

__declspec(dllexport) uint32_t Supports(void) { return 1; }

BOOL WINAPI DllMain(HINSTANCE h, DWORD reason, LPVOID r)
{
    (void)r;
    if (reason == DLL_PROCESS_ATTACH) DisableThreadLibraryCalls(h);
    return TRUE;
}
