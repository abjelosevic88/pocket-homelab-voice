#!/usr/bin/env python3
"""Who spoke when, for Pocket Voice. Runs on this machine with onnxruntime.

Reads one JSON request on stdin and writes one JSON answer on stdout:

  {"cmd": "diarize", "wav": "...16 kHz mono WAV", "seg_model": "...", "emb_model": "...",
   "threshold": 0.5, "step_s": 2.5, "threads": 4}
  → {"turns": [{"s": ms, "e": ms, "k": n}], "speakers": [{"k": n, "ms": total, "emb": [...], "n": votes, "spread": x}], "dim": d}

  {"cmd": "embed", "wav": "...", "emb_model": "...", "spans": [[from_ms, to_ms], ...]}
  → {"embs": [[...] or null], "dim": d}

Diarization follows pyannote: a segmentation model finds up to three local
speakers in each 10 s window, each local speaker gets a voice embedding from
the stretches where only they speak, the embeddings are clustered into the
people of the recording, and the windows are stitched back together.
Embeddings are WeSpeaker models on 80-band log mel filterbanks with the mean
taken out, L2-normalised."""
import json, sys, wave
import numpy as np
import onnxruntime as ort

RATE, FRAME, SHIFT, FFT, MELS = 16000, 400, 160, 512, 80


# ---------- features ----------

def _mel_banks():
    mel = lambda f: 1127.0 * np.log(1.0 + f / 700.0)
    centers = np.linspace(mel(20.0), mel(RATE / 2), MELS + 2)
    fft_mel = mel(np.arange(FFT // 2) * RATE / FFT)
    banks = np.zeros((MELS, FFT // 2 + 1))
    for m in range(MELS):
        l, c, r = centers[m], centers[m + 1], centers[m + 2]
        banks[m, :FFT // 2] = np.maximum(0.0, np.minimum((fft_mel - l) / (c - l), (r - fft_mel) / (r - c)))
    return banks


BANKS = _mel_banks().T
WINDOW = (0.5 - 0.5 * np.cos(2 * np.pi * np.arange(FRAME) / (FRAME - 1))) ** 0.85  # povey


def fbank(x):
    """Kaldi-style log mel filterbank of samples in int16 scale, mean taken out."""
    n = 1 + (len(x) - FRAME) // SHIFT
    if n < 1:
        return None
    idx = np.arange(FRAME)[None, :] + SHIFT * np.arange(n)[:, None]
    fr = x[idx].astype(np.float64)
    fr -= fr.mean(axis=1, keepdims=True)
    fr[:, 1:] -= 0.97 * fr[:, :-1].copy()
    fr[:, 0] *= 0.03
    fr *= WINDOW
    spec = np.abs(np.fft.rfft(fr, n=FFT)) ** 2
    f = np.log(np.maximum(spec @ BANKS, 1.1920929e-07))
    f -= f.mean(axis=0, keepdims=True)
    return f.astype(np.float32)


class Embedder:
    def __init__(self, path, threads):
        o = ort.SessionOptions()
        o.intra_op_num_threads = threads
        o.inter_op_num_threads = 1
        self.s = ort.InferenceSession(path, o, providers=["CPUExecutionProvider"])
        self.inp = self.s.get_inputs()[0].name
        self.scale = 1.0 / 32768 if self.s.get_modelmeta().custom_metadata_map.get("normalize_samples") == "1" else 1.0

    def __call__(self, x):
        """x: float samples in int16 scale; at least 0.5 s."""
        f = fbank(x * self.scale)
        if f is None or len(f) < 50:
            return None
        v = self.s.run(None, {self.inp: f[None]})[0][0].astype(np.float64)
        nv = np.linalg.norm(v)
        return None if not np.isfinite(nv) or nv == 0 else v / nv


def read_wav(path):
    with wave.open(path, "rb") as w:
        if w.getframerate() != RATE or w.getnchannels() != 1 or w.getsampwidth() != 2:
            raise SystemExit("want a 16 kHz mono 16-bit WAV")
        return np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype(np.float32)


# ---------- segmentation ----------

def powerset(num_speakers=3):
    rows = [[0] * num_speakers]
    for j in range(num_speakers):
        r = [0] * num_speakers; r[j] = 1; rows.append(r)
    for j in range(num_speakers):
        for m in range(j + 1, num_speakers):
            r = [0] * num_speakers; r[j] = r[m] = 1; rows.append(r)
    return np.array(rows, dtype=np.int8)


def segment(pcm, path, step, threads):
    """Local speaker activity per window: starts, (windows x frames x 3) 0/1, window size."""
    o = ort.SessionOptions()
    o.intra_op_num_threads = threads
    o.inter_op_num_threads = 1
    s = ort.InferenceSession(path, o, providers=["CPUExecutionProvider"])
    meta = s.get_modelmeta().custom_metadata_map
    win = int(meta.get("window_size", 160000))
    ps = powerset(int(meta.get("num_speakers", 3)))
    x = pcm / 32768.0
    starts = list(range(0, max(1, len(x) - win + 1), step))
    if len(x) > win and (len(x) - win) % step:
        starts.append(len(x) - win)
    labs, batch = [], 8
    for b in range(0, len(starts), batch):
        chunk = np.zeros((len(starts[b:b + batch]), 1, win), dtype=np.float32)
        for i, st in enumerate(starts[b:b + batch]):
            seg = x[st:st + win]
            chunk[i, 0, :len(seg)] = seg
        y = s.run(None, {s.get_inputs()[0].name: chunk})[0]
        labs.extend(ps[y[i].argmax(axis=1)] for i in range(len(y)))
    return np.array(starts), np.array(labs), win


# ---------- clustering ----------

def cluster(E, threshold):
    """Agglomerative clustering on cosine similarity with centroid linkage:
    the two most similar groups merge while their similarity is above threshold."""
    n = len(E)
    sums = E.copy()
    alive = np.ones(n, dtype=bool)
    label = np.arange(n)
    S = E @ E.T
    np.fill_diagonal(S, -2)
    while n > 1:
        i, j = np.unravel_index(np.argmax(S), S.shape)
        if S[i, j] < threshold:
            break
        sums[i] += sums[j]
        label[label == j] = i
        alive[j] = False
        S[j, :] = -2
        S[:, j] = -2
        c = sums[i] / np.linalg.norm(sums[i])
        sim = (sums / np.maximum(np.linalg.norm(sums, axis=1, keepdims=True), 1e-9)) @ c
        sim[~alive] = -2
        sim[i] = -2
        S[i, :] = sim
        S[:, i] = sim
    return np.unique(label, return_inverse=True)[1]


def refine(E, label, weight, min_weight):
    """Fold groups with too little speech into the nearest real one, then give
    every embedding to its closest group (two rounds)."""
    for _ in range(2):
        ks = np.unique(label)
        cent = {k: E[label == k].sum(0) for k in ks}
        big = [k for k in ks if weight[label == k].sum() >= min_weight] or [max(ks, key=lambda k: weight[label == k].sum())]
        M = np.array([cent[k] / np.linalg.norm(cent[k]) for k in big])
        label = np.array(big)[np.argmax(E @ M.T, axis=1)]
    return np.unique(label, return_inverse=True)[1]


# ---------- diarization ----------

def analyse(req):
    """The slow part: segmentation and one embedding per (window, local speaker)
    from where only that speaker talks."""
    pcm = read_wav(req["wav"])
    threads = int(req.get("threads") or 4)
    step = int(float(req.get("step_s") or 5) * RATE)
    starts, labs, win = segment(pcm, req["seg_model"], step, threads)
    emb = Embedder(req["emb_model"], threads)
    fs = win / labs.shape[1]  # samples per frame
    cap = int(float(req.get("max_emb_s") or 5) * RATE)
    keys, E, W = [], [], []
    for ci in range(len(starts)):
        st, lab = starts[ci], labs[ci]
        alone = lab * (lab.sum(axis=1, keepdims=True) == 1)
        for spk in range(lab.shape[1]):
            act = alone[:, spk]
            if act.sum() * fs < 0.8 * RATE:
                continue
            idx = np.flatnonzero(act)
            breaks = np.flatnonzero(np.diff(idx) > 1)
            runs = zip(np.concatenate([[idx[0]], idx[breaks + 1]]), np.concatenate([idx[breaks], [idx[-1]]]))
            # the longest stretches, up to max_s of audio: enough for a voice print, and quick
            runs = sorted(runs, key=lambda r: r[0] - r[1])
            parts, got = [], 0
            for a, b in runs:
                x = pcm[st + int(a * fs): st + int((b + 1) * fs)][: cap - got]
                parts.append(x)
                got += len(x)
                if got >= cap:
                    break
            v = emb(np.concatenate(parts))
            if v is not None:
                keys.append((ci, spk)); E.append(v); W.append(act.sum() * fs / RATE)
    return dict(n=len(pcm), starts=starts, labs=labs, win=win, step=step,
                keys=np.array(keys, dtype=int).reshape(-1, 2), E=np.array(E).reshape(len(E), -1), W=np.array(W))


def assemble(a, threshold, min_speaker_s):
    """Cluster the embeddings into people and stitch the windows together."""
    starts, labs, keys, E, W = a["starts"], a["labs"], a["keys"], a["E"], a["W"]
    if len(E) == 0:
        return {"turns": [], "speakers": [], "dim": 0}
    label = refine(E, cluster(E, threshold), W, min_speaker_s)
    K = label.max() + 1
    who = {(int(c), int(s)): int(l) for (c, s), l in zip(keys, label)}
    frames = labs.shape[1]
    fs = a["win"] / frames
    total = int(np.ceil(a["n"] / fs)) + 1
    count, weight = np.zeros(total), np.zeros(total)
    act = np.zeros((total, K))
    for ci in range(len(starts)):
        g = int(round(starts[ci] / fs))
        n = max(0, min(frames, total - g))
        lab = labs[ci][:n]
        count[g:g + n] += lab.sum(axis=1)
        weight[g:g + n] += 1
        for spk in range(lab.shape[1]):
            k = who.get((ci, spk))
            if k is not None:
                act[g:g + n, k] += lab[:, spk]
    nspk = np.floor(count / np.maximum(weight, 1e-9) + 0.5).astype(int)
    final = np.zeros(act.shape, dtype=bool)
    for t in np.flatnonzero(nspk):
        top = np.argsort(-act[t])[:nspk[t]]
        final[t, top[act[t, top] > 0]] = True

    # turns, with short gaps closed and short blips dropped
    turns = []
    to_ms = fs * 1000 / RATE
    for k in range(K):
        edges = np.flatnonzero(np.diff(np.concatenate([[0], final[:, k].astype(int), [0]])))
        mine = []
        for s, e in zip(edges[::2], edges[1::2]):
            s, e = int(s * to_ms), int(e * to_ms)
            if mine and s - mine[-1]["e"] < 500:
                mine[-1]["e"] = e
            else:
                mine.append({"s": s, "e": e, "k": k})
        turns += [t for t in mine if t["e"] - t["s"] >= 300]
    turns.sort(key=lambda t: t["s"])

    speakers = []
    for k in range(K):
        m = label == k
        c = E[m].sum(0)
        c /= np.linalg.norm(c)
        speakers.append({"k": k, "ms": sum(t["e"] - t["s"] for t in turns if t["k"] == k), "n": int(m.sum()),
                         "spread": round(float(1 - (E[m] @ c).mean()), 4), "emb": [round(float(v), 5) for v in c]})
    return {"turns": turns, "speakers": speakers, "dim": int(E.shape[1])}


def diarize(req):
    cache = req.get("cache")
    if cache:
        try:
            a = dict(np.load(cache))
        except OSError:
            a = analyse(req)
            np.savez(cache, **a)
    else:
        a = analyse(req)
    return assemble(a, float(req.get("threshold") or 0.5), float(req.get("min_speaker_s") or 20))


def embed(req):
    pcm = read_wav(req["wav"])
    emb = Embedder(req["emb_model"], int(req.get("threads") or 4))
    out = []
    for s, e in req["spans"]:
        v = emb(pcm[int(s * RATE / 1000): int(e * RATE / 1000)]) if e - s >= 800 else None
        out.append(None if v is None else [round(float(t), 5) for t in v])
    return {"embs": out, "dim": len(next((v for v in out if v), []))}


if __name__ == "__main__":
    req = json.load(sys.stdin)
    json.dump({"diarize": diarize, "embed": embed}[req.get("cmd", "diarize")](req), sys.stdout)
