class PCMPlayer extends AudioWorkletProcessor {
  constructor() {
    super();
    this.chunks = [];
    this.readOff = 0;
    this.queued = 0;
    this.gen = 0;
    this.underrun = 0;
    this.lastReport = 0;
    this.lastStat = 0;
    this.lowSent = false;
    this.got = 0;
    this.playing = false;
    this.port.onmessage = (e) => {
      const d = e.data || {};
      if (d.type === "reset") {
        const qs = sampleRate > 0 ? this.queued / sampleRate : 0;
        const g = d.gen != null ? d.gen : this.gen;
        this.chunks = [];
        this.readOff = 0;
        this.queued = 0;
        this.lowSent = false;
        this.playing = false;
        this.gen = g;
        this.port.postMessage({ type: "drain", gen: g, qs: qs });
        return;
      }
      if (d.type === "start") {
        if (d.gen != null && d.gen < this.gen) return;
        if (d.gen != null) this.gen = d.gen;
        this.playing = true;
        return;
      }
      if (d.type === "hold") {
        if (d.gen != null && d.gen < this.gen) return;
        this.playing = false;
        return;
      }
      if (d.type === "pcm" && d.samples) {
        if (d.gen != null && d.gen !== this.gen) {
          if (d.gen < this.gen) return;
          this.gen = d.gen;
          this.chunks = [];
          this.readOff = 0;
          this.queued = 0;
        }
        const pcm = d.samples.length != null ? d.samples : new Float32Array(d.samples);
        if (!pcm.length) return;
        this.chunks.push(pcm);
        this.queued += pcm.length >> 1;
        this.got += 1;
        if (this.queued > sampleRate * 0.12) this.lowSent = false;
      }
    };
  }
  process(_inputs, outputs) {
    const out = outputs[0];
    if (!out || !out.length) return true;
    const left = out[0];
    const right = out[1] || out[0];
    if (!this.playing) {
      left.fill(0);
      if (right !== left) right.fill(0);
      if (currentFrame - this.lastStat > sampleRate * 0.05) {
        this.lastStat = currentFrame;
        this.port.postMessage({ type: "stat", qs: this.queued / sampleRate, got: this.got, n: this.underrun, hold: 1 });
      }
      return true;
    }
    const n = left.length;
    let i = 0;
    let starved = 0;
    while (i < n) {
      if (!this.chunks.length) {
        left.fill(0, i);
        if (right !== left) right.fill(0, i);
        starved += n - i;
        this.underrun += 1;
        break;
      }
      const cur = this.chunks[0];
      const avail = (cur.length - this.readOff) >> 1;
      if (avail < 1) {
        this.chunks.shift();
        this.readOff = 0;
        continue;
      }
      const take = Math.min(avail, n - i);
      for (let k = 0; k < take; k++) {
        left[i + k] = cur[this.readOff];
        right[i + k] = cur[this.readOff + 1];
        this.readOff += 2;
      }
      this.queued -= take;
      i += take;
    }
    if (this.queued < 0) this.queued = 0;
    const qs = this.queued / sampleRate;
    if (currentFrame - this.lastStat > sampleRate * 0.05) {
      this.lastStat = currentFrame;
      this.port.postMessage({ type: "stat", qs: qs, got: this.got, n: this.underrun });
    }
    if (starved && currentFrame - this.lastReport > sampleRate * 0.4) {
      this.lastReport = currentFrame;
      this.port.postMessage({ type: "underrun", n: this.underrun, cb: starved, qs: qs, got: this.got, nq: this.chunks.length });
    }
    if (!starved && this.queued < sampleRate * 0.08 && !this.lowSent) {
      this.lowSent = true;
      this.port.postMessage({ type: "low", qs: qs });
    }
    return true;
  }
}
registerProcessor("shorts-pcm", PCMPlayer);
