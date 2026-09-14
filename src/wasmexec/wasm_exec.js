// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

"use strict";

(() => {
	const enosys = () => {
		const err = new Error("not implemented");
		err.code = "ENOSYS";
		return err;
	};

	// flushConsole writes out buffered, not yet newline-terminated output of
	// the fallback fs shim below. It is called when the Go program exits, so
	// that trailing output is not lost. It is a no-op if a real fs is present.
	let flushConsole = () => {};

	if (!globalThis.fs) {
		const outputBufs = new Map(); // per-fd buffers for incomplete lines (1: stdout, 2: stderr)
		const outputDecoders = new Map(); // per-fd streaming UTF-8 decoders, so runes split across writes survive
		const writeLine = (fd, line) => {
			(fd === 2 ? console.error : console.log)(line);
		};
		flushConsole = () => {
			// Flush each decoder's pending bytes (an incomplete trailing
			// rune decodes to U+FFFD), then the buffered partial lines.
			for (const [fd, dec] of outputDecoders) {
				const tail = dec.decode();
				if (tail.length > 0) {
					outputBufs.set(fd, (outputBufs.get(fd) ?? "") + tail);
				}
			}
			outputDecoders.clear();
			for (const [fd, buf] of outputBufs) {
				if (buf.length > 0) {
					writeLine(fd, buf);
				}
			}
			outputBufs.clear();
		};
		globalThis.fs = {
			constants: { O_WRONLY: -1, O_RDWR: -1, O_CREAT: -1, O_TRUNC: -1, O_APPEND: -1, O_EXCL: -1, O_DIRECTORY: -1 }, // unused
			writeSync(fd, buf) {
				let dec = outputDecoders.get(fd);
				if (dec === undefined) {
					dec = new TextDecoder("utf-8");
					outputDecoders.set(fd, dec);
				}
				let outputBuf = (outputBufs.get(fd) ?? "") + dec.decode(buf, { stream: true });
				const nl = outputBuf.lastIndexOf("\n");
				if (nl != -1) {
					writeLine(fd, outputBuf.substring(0, nl));
					outputBuf = outputBuf.substring(nl + 1);
				}
				outputBufs.set(fd, outputBuf);
				return buf.length;
			},
			write(fd, buf, offset, length, position, callback) {
				if (offset !== 0 || length !== buf.length || position !== null) {
					callback(enosys());
					return;
				}
				const n = this.writeSync(fd, buf);
				callback(null, n);
			},
			chmod(path, mode, callback) { callback(enosys()); },
			chown(path, uid, gid, callback) { callback(enosys()); },
			close(fd, callback) { callback(enosys()); },
			fchmod(fd, mode, callback) { callback(enosys()); },
			fchown(fd, uid, gid, callback) { callback(enosys()); },
			fstat(fd, callback) { callback(enosys()); },
			fsync(fd, callback) { callback(null); },
			ftruncate(fd, length, callback) { callback(enosys()); },
			lchown(path, uid, gid, callback) { callback(enosys()); },
			link(path, link, callback) { callback(enosys()); },
			lstat(path, callback) { callback(enosys()); },
			mkdir(path, perm, callback) { callback(enosys()); },
			open(path, flags, mode, callback) { callback(enosys()); },
			read(fd, buffer, offset, length, position, callback) { callback(enosys()); },
			readdir(path, callback) { callback(enosys()); },
			readlink(path, callback) { callback(enosys()); },
			rename(from, to, callback) { callback(enosys()); },
			rmdir(path, callback) { callback(enosys()); },
			stat(path, callback) { callback(enosys()); },
			symlink(path, link, callback) { callback(enosys()); },
			truncate(path, length, callback) { callback(enosys()); },
			unlink(path, callback) { callback(enosys()); },
			utimes(path, atime, mtime, callback) { callback(enosys()); },
		};
	}

	if (!globalThis.process) {
		globalThis.process = {
			getuid() { return -1; },
			getgid() { return -1; },
			geteuid() { return -1; },
			getegid() { return -1; },
			getgroups() { throw enosys(); },
			pid: -1,
			ppid: -1,
			umask() { throw enosys(); },
			cwd() { throw enosys(); },
			chdir() { throw enosys(); },
		}
	}

	if (!globalThis.path) {
		globalThis.path = {
			resolve(...pathSegments) {
				return pathSegments.join("/");
			}
		}
	}

	if (!globalThis.crypto) {
		throw new Error("globalThis.crypto is not available, polyfill required (crypto.getRandomValues only)");
	}

	if (!globalThis.performance) {
		throw new Error("globalThis.performance is not available, polyfill required (performance.now only)");
	}

	if (!globalThis.TextEncoder) {
		throw new Error("globalThis.TextEncoder is not available, polyfill required");
	}

	if (!globalThis.TextDecoder) {
		throw new Error("globalThis.TextDecoder is not available, polyfill required");
	}

	const encoder = new TextEncoder("utf-8");
	const decoder = new TextDecoder("utf-8");

	// typedArrays maps the TypedArray kind ids used by syscall/js.copyToGo
	// and syscall/js.copyToJS to their constructors. Keep in sync with the
	// typedArrayKind constants in src/syscall/js/js.go.
	const typedArrays = [Int8Array, Uint8Array, Int16Array, Uint16Array, Int32Array, Uint32Array, Float32Array, Float64Array];

	globalThis.Go = class {
		constructor() {
			this.argv = ["js"];
			this.env = {};
			this.exit = (code) => {
				if (code !== 0) {
					console.warn("exit code:", code);
				}
			};
			this._exitPromise = new Promise((resolve) => {
				this._resolveExitPromise = resolve;
			});
			this._pendingEvent = null;
			this._scheduledTimeouts = new Map();
			this._nextCallbackTimeoutID = 1;

			const setInt64 = (addr, v) => {
				this.mem.setUint32(addr + 0, v, true);
				this.mem.setUint32(addr + 4, Math.floor(v / 4294967296), true);
			}

			const setInt32 = (addr, v) => {
				this.mem.setUint32(addr + 0, v, true);
			}

			const getInt64 = (addr) => {
				const low = this.mem.getUint32(addr + 0, true);
				const high = this.mem.getInt32(addr + 4, true);
				return low + high * 4294967296;
			}

			const loadValue = (addr) => {
				const f = this.mem.getFloat64(addr, true);
				if (f === 0) {
					return undefined;
				}
				if (!isNaN(f)) {
					return f;
				}

				const id = this.mem.getUint32(addr, true);
				return this._values[id];
			}

			const storeValue = (addr, v) => {
				const nanHead = 0x7FF80000;

				if (typeof v === "number" && v !== 0) {
					if (isNaN(v)) {
						this.mem.setUint32(addr + 4, nanHead, true);
						this.mem.setUint32(addr, 0, true);
						return;
					}
					this.mem.setFloat64(addr, v, true);
					return;
				}

				if (v === undefined) {
					this.mem.setFloat64(addr, 0, true);
					return;
				}

				let id = this._ids.get(v);
				if (id === undefined) {
					id = this._idPool.pop();
					if (id === undefined) {
						id = this._values.length;
					}
					this._values[id] = v;
					this._goRefCounts[id] = 0;
					this._ids.set(v, id);
				}
				this._goRefCounts[id]++;
				let typeFlag = 0;
				switch (typeof v) {
					case "object":
						if (v !== null) {
							typeFlag = 1;
						}
						break;
					case "string":
						typeFlag = 2;
						break;
					case "symbol":
						typeFlag = 3;
						break;
					case "function":
						typeFlag = 4;
						break;
				}
				this.mem.setUint32(addr + 4, nanHead | typeFlag, true);
				this.mem.setUint32(addr, id, true);
			}

			const loadSlice = (addr) => {
				const array = getInt64(addr + 0);
				const len = getInt64(addr + 8);
				return new Uint8Array(this._mem().buffer, array, len);
			}

			const loadSliceOfValues = (addr) => {
				const array = getInt64(addr + 0);
				const len = getInt64(addr + 8);
				const a = new Array(len);
				for (let i = 0; i < len; i++) {
					a[i] = loadValue(array + i * 8);
				}
				return a;
			}

			const loadString = (addr) => {
				const saddr = getInt64(addr + 0);
				const len = getInt64(addr + 8);
				let view = new Uint8Array(this._mem().buffer, saddr, len);
				if (this._sharedMem) {
					// TextDecoder.decode rejects views backed by a
					// SharedArrayBuffer; decode a copy instead.
					view = view.slice();
				}
				return decoder.decode(view);
			}

			const testCallExport = (a, b) => {
				this._inst.exports.testExport0();
				return this._inst.exports.testExport(a, b);
			}

			// scheduleTimeout schedules the program to be resumed after delay
			// milliseconds and returns the timeout id. A weak timeout does not
			// keep the host's event loop (and with it the process) alive while
			// it is pending: on Node.js the timer is unref'd; browsers have no
			// such notion, so there a weak timeout is a plain setTimeout.
			const scheduleTimeout = (delay, weak) => {
				const id = this._nextCallbackTimeoutID;
				this._nextCallbackTimeoutID++;
				const timer = setTimeout(
					() => {
						this._resume();
						// Under GOWASM=threads (shared memory) the runtime may
						// clear the timeout record asynchronously - a resumed
						// main thread without a free P defers the event
						// goroutine - so the missed-timeout retry only applies
						// to the single-threaded runtime.
						while (!this._sharedMem && this._scheduledTimeouts.has(id)) {
							// for some reason Go failed to register the timeout event, log and try again
							// (temporary workaround for https://github.com/golang/go/issues/28975)
							console.warn("scheduleTimeoutEvent: missed timeout event");
							this._resume();
						}
					},
					delay,
				);
				if (weak && typeof timer?.unref === "function") {
					timer.unref();
				}
				this._scheduledTimeouts.set(id, timer);
				return id;
			}

			const timeOrigin = Date.now() - performance.now();
			// Exposed for GOWASM=threads worker threads: workers must use the
			// same nanotime base as the main instance so the runtime's clock
			// is consistent across Ms (see wasm_exec_node.js).
			this._timeOrigin = timeOrigin;
			this.importObject = {
				_gotest: {
					add: (a, b) => a + b,
					callExport: testCallExport,
				},
				gojs: {
					// Go's SP does not change as long as no Go code is running. Some operations (e.g. calls, getters and setters)
					// may synchronously trigger a Go event handler. This makes Go code get executed in the middle of the imported
					// function. A goroutine can switch to a new stack if the current stack is too small (see morestack function).
					// This changes the SP, thus we have to update the SP used by the imported function.

					// func wasmExit(code int32)
					"runtime.wasmExit": (sp) => {
						sp >>>= 0;
						const code = this.mem.getInt32(sp + 8, true);
						this.exited = true;
						if (this._keepAliveTimer !== undefined) {
							clearInterval(this._keepAliveTimer);
							this._keepAliveTimer = undefined;
						}
						delete this._inst;
						delete this._values;
						delete this._goRefCounts;
						delete this._ids;
						delete this._idPool;
						flushConsole();
						this.exit(code);
					},

					// func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)
					"runtime.wasmWrite": (sp) => {
						sp >>>= 0;
						const fd = getInt64(sp + 8);
						const p = getInt64(sp + 16);
						const n = this.mem.getInt32(sp + 24, true);
						let buf = new Uint8Array(this._mem().buffer, p, n);
						if (this._sharedMem) {
							// Node's fs.writeSync rejects views backed by a
							// SharedArrayBuffer; write a copy instead.
							buf = buf.slice();
						}
						fs.writeSync(fd, buf);
					},

					// func resetMemoryDataView()
					"runtime.resetMemoryDataView": (sp) => {
						sp >>>= 0;
						this.mem = new DataView(this._mem().buffer);
					},

					// func nanotime1() int64
					"runtime.nanotime1": (sp) => {
						sp >>>= 0;
						setInt64(sp + 8, (timeOrigin + performance.now()) * 1000000);
					},

					// func walltime() (sec int64, nsec int32)
					"runtime.walltime": (sp) => {
						sp >>>= 0;
						const msec = (new Date).getTime();
						setInt64(sp + 8, msec / 1000);
						this.mem.setInt32(sp + 16, (msec % 1000) * 1000000, true);
					},

					// func scheduleTimeoutEvent(delay int64) int32
					"runtime.scheduleTimeoutEvent": (sp) => {
						sp >>>= 0;
						this.mem.setInt32(sp + 16, scheduleTimeout(getInt64(sp + 8), false), true);
					},

					// func scheduleWeakTimeoutEvent(delay int64) int32
					"runtime.scheduleWeakTimeoutEvent": (sp) => {
						sp >>>= 0;
						this.mem.setInt32(sp + 16, scheduleTimeout(getInt64(sp + 8), true), true);
					},

					// func clearTimeoutEvent(id int32)
					"runtime.clearTimeoutEvent": (sp) => {
						sp >>>= 0;
						const id = this.mem.getInt32(sp + 8, true);
						clearTimeout(this._scheduledTimeouts.get(id));
						this._scheduledTimeouts.delete(id);
					},

					// func wasmMainWakeInit(addr *uint32)
					//
					// GOWASM=threads only: registers the runtime's main-thread
					// wake word and arms the Atomics.waitAsync watcher on it
					// (see _armMainWake). Worker threads bump and notify the
					// word to wake a main M parked in the event loop.
					"runtime.wasmMainWakeInit": (sp) => {
						sp >>>= 0;
						this._mainWakeAddr = getInt64(sp + 8);
						this._armMainWake();
					},

					// func wasmSetKeepAlive(on int32)
					//
					// GOWASM=threads only: while on, hold the event loop open
					// with an interval timer. Worker threads running Go code
					// are unref'd and a parked main thread schedules nothing,
					// so without this the process could exit underneath
					// running Go code. The runtime turns it off when the
					// program is fully idle, so the exit-time deadlock probe
					// still fires.
					"runtime.wasmSetKeepAlive": (sp) => {
						sp >>>= 0;
						const on = this.mem.getInt32(sp + 8, true) !== 0;
						if (on && this._keepAliveTimer === undefined) {
							this._keepAliveTimer = setInterval(() => { }, 0x7fffffff);
						} else if (!on && this._keepAliveTimer !== undefined) {
							clearInterval(this._keepAliveTimer);
							this._keepAliveTimer = undefined;
						}
					},

					// func getRandomData(r []byte)
					"runtime.getRandomData": (sp) => {
						sp >>>= 0;
						const slice = loadSlice(sp + 8);
						if (this._sharedMem) {
							// crypto.getRandomValues rejects views backed by
							// a SharedArrayBuffer; fill a copy and write it
							// back.
							const tmp = new Uint8Array(slice.length);
							crypto.getRandomValues(tmp);
							slice.set(tmp);
						} else {
							crypto.getRandomValues(slice);
						}
					},

					// func finalizeRef(v ref)
					"syscall/js.finalizeRef": (sp) => {
						sp >>>= 0;
						const id = this.mem.getUint32(sp + 8, true);
						this._goRefCounts[id]--;
						if (this._goRefCounts[id] === 0) {
							const v = this._values[id];
							this._values[id] = null;
							this._ids.delete(v);
							this._idPool.push(id);
						}
					},

					// func stringVal(value string) ref
					"syscall/js.stringVal": (sp) => {
						sp >>>= 0;
						storeValue(sp + 24, loadString(sp + 8));
					},

					// func valueGet(v ref, p string) ref
					"syscall/js.valueGet": (sp) => {
						sp >>>= 0;
						const result = Reflect.get(loadValue(sp + 8), loadString(sp + 16));
						sp = this._inst.exports.getsp() >>> 0; // see comment above
						storeValue(sp + 32, result);
					},

					// func valueSet(v ref, p string, x ref)
					"syscall/js.valueSet": (sp) => {
						sp >>>= 0;
						Reflect.set(loadValue(sp + 8), loadString(sp + 16), loadValue(sp + 32));
					},

					// func valueDelete(v ref, p string)
					"syscall/js.valueDelete": (sp) => {
						sp >>>= 0;
						Reflect.deleteProperty(loadValue(sp + 8), loadString(sp + 16));
					},

					// func valueIndex(v ref, i int) ref
					"syscall/js.valueIndex": (sp) => {
						sp >>>= 0;
						const result = Reflect.get(loadValue(sp + 8), getInt64(sp + 16));
						sp = this._inst.exports.getsp() >>> 0; // see comment above
						storeValue(sp + 24, result);
					},

					// valueSetIndex(v ref, i int, x ref)
					"syscall/js.valueSetIndex": (sp) => {
						sp >>>= 0;
						Reflect.set(loadValue(sp + 8), getInt64(sp + 16), loadValue(sp + 24));
					},

					// func valueCall(v ref, m string, args []ref) (ref, bool)
					"syscall/js.valueCall": (sp) => {
						sp >>>= 0;
						try {
							const v = loadValue(sp + 8);
							const m = Reflect.get(v, loadString(sp + 16));
							const args = loadSliceOfValues(sp + 32);
							const result = Reflect.apply(m, v, args);
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 56, result);
							this.mem.setUint8(sp + 64, 1);
						} catch (err) {
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 56, err);
							this.mem.setUint8(sp + 64, 0);
						}
					},

					// func valueInvoke(v ref, args []ref) (ref, bool)
					"syscall/js.valueInvoke": (sp) => {
						sp >>>= 0;
						try {
							const v = loadValue(sp + 8);
							const args = loadSliceOfValues(sp + 16);
							const result = Reflect.apply(v, undefined, args);
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 40, result);
							this.mem.setUint8(sp + 48, 1);
						} catch (err) {
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 40, err);
							this.mem.setUint8(sp + 48, 0);
						}
					},

					// func valueNew(v ref, args []ref) (ref, bool)
					"syscall/js.valueNew": (sp) => {
						sp >>>= 0;
						try {
							const v = loadValue(sp + 8);
							const args = loadSliceOfValues(sp + 16);
							const result = Reflect.construct(v, args);
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 40, result);
							this.mem.setUint8(sp + 48, 1);
						} catch (err) {
							sp = this._inst.exports.getsp() >>> 0; // see comment above
							storeValue(sp + 40, err);
							this.mem.setUint8(sp + 48, 0);
						}
					},

					// func valueLength(v ref) int
					"syscall/js.valueLength": (sp) => {
						sp >>>= 0;
						const len = parseInt(loadValue(sp + 8).length) || 0; // no length property gives 0, not NaN
						sp = this._inst.exports.getsp() >>> 0; // see comment above
						setInt64(sp + 16, len);
					},

					// valuePrepareString(v ref) (ref, int)
					"syscall/js.valuePrepareString": (sp) => {
						sp >>>= 0;
						const str = encoder.encode(String(loadValue(sp + 8)));
						storeValue(sp + 16, str);
						setInt64(sp + 24, str.length);
					},

					// valueLoadString(v ref, b []byte)
					"syscall/js.valueLoadString": (sp) => {
						sp >>>= 0;
						const str = loadValue(sp + 8);
						loadSlice(sp + 16).set(str);
					},

					// func valueInstanceOf(v ref, t ref) bool
					"syscall/js.valueInstanceOf": (sp) => {
						sp >>>= 0;
						this.mem.setUint8(sp + 24, (loadValue(sp + 8) instanceof loadValue(sp + 16)) ? 1 : 0);
					},

					// func copyBytesToGo(dst []byte, src ref) (int, bool)
					"syscall/js.copyBytesToGo": (sp) => {
						sp >>>= 0;
						const dst = loadSlice(sp + 8);
						const src = loadValue(sp + 32);
						if (!(src instanceof Uint8Array || src instanceof Uint8ClampedArray)) {
							this.mem.setUint8(sp + 48, 0);
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						setInt64(sp + 40, toCopy.length);
						this.mem.setUint8(sp + 48, 1);
					},

					// func copyBytesToJS(dst ref, src []byte) (int, bool)
					"syscall/js.copyBytesToJS": (sp) => {
						sp >>>= 0;
						const dst = loadValue(sp + 8);
						const src = loadSlice(sp + 16);
						if (!(dst instanceof Uint8Array || dst instanceof Uint8ClampedArray)) {
							this.mem.setUint8(sp + 48, 0);
							return;
						}
						const toCopy = src.subarray(0, dst.length);
						dst.set(toCopy);
						setInt64(sp + 40, toCopy.length);
						this.mem.setUint8(sp + 48, 1);
					},

					// func copyToGo(dst unsafe.Pointer, dstLen int, kind int, src ref) (int, bool)
					"syscall/js.copyToGo": (sp) => {
						sp >>>= 0;
						const dst = getInt64(sp + 8);
						const dstLen = getInt64(sp + 16);
						const kind = getInt64(sp + 24);
						const src = loadValue(sp + 32);
						if (!(src instanceof typedArrays[kind])) {
							this.mem.setUint8(sp + 48, 0);
							return;
						}
						const toCopy = src.subarray(0, dstLen);
						// Create the view of the Go slice only now, from the
						// current memory buffer: an earlier view would be
						// stale (detached) if memory has grown since.
						new typedArrays[kind](this._mem().buffer, dst, toCopy.length).set(toCopy);
						setInt64(sp + 40, toCopy.length);
						this.mem.setUint8(sp + 48, 1);
					},

					// func copyToJS(dst ref, src unsafe.Pointer, srcLen int, kind int) (int, bool)
					"syscall/js.copyToJS": (sp) => {
						sp >>>= 0;
						const dst = loadValue(sp + 8);
						const src = getInt64(sp + 16);
						const srcLen = getInt64(sp + 24);
						const kind = getInt64(sp + 32);
						if (!(dst instanceof typedArrays[kind])) {
							this.mem.setUint8(sp + 48, 0);
							return;
						}
						const n = Math.min(srcLen, dst.length);
						// See copyToGo about view freshness.
						dst.set(new typedArrays[kind](this._mem().buffer, src, n));
						setInt64(sp + 40, n);
						this.mem.setUint8(sp + 48, 1);
					},

					"debug": (value) => {
						console.log(value);
					},
				}
			};
		}

		// _mem returns the WebAssembly.Memory the program uses: the host
		// -created imported memory of a GOWASM=threads module (see
		// provideMemory), or the module's own exported memory otherwise.
		_mem() {
			return this._importedMemory ?? this._inst.exports.mem;
		}

		// provideMemory inspects the given wasm module bytes (a Uint8Array)
		// and, if the module imports its linear memory ("gojs"."mem" -
		// GOWASM=threads builds do this instead of declaring their own),
		// creates a matching WebAssembly.Memory and adds it to importObject.
		// It must be called before instantiating such a module; for modules
		// that declare their own memory it is a no-op. Returns the created
		// memory, or undefined.
		//
		// Note: a shared memory is backed by a SharedArrayBuffer. Node.js
		// needs no flags for this; browsers require cross-origin isolation
		// (COOP/COEP response headers) for SharedArrayBuffer to exist.
		provideMemory(bytes) {
			const imp = this._findMemoryImport(bytes);
			if (imp === undefined) {
				return undefined;
			}
			const descriptor = { initial: imp.initial };
			if (imp.hasMax) {
				descriptor.maximum = imp.maximum;
			}
			if (imp.shared) {
				descriptor.shared = true;
			}
			this._importedMemory = new WebAssembly.Memory(descriptor);
			this.importObject[imp.module][imp.name] = this._importedMemory;
			return this._importedMemory;
		}

		// _findMemoryImport scans a wasm binary's import section for a
		// linear memory imported from the "gojs" module and returns its
		// limits, or undefined. (The JS API offers no way to read an
		// import's limits from a compiled module, hence the manual scan.)
		_findMemoryImport(bytes) {
			let off = 0;
			const byte = () => {
				if (off >= bytes.length) {
					throw new Error("provideMemory: truncated wasm binary");
				}
				return bytes[off++];
			};
			const uleb = () => {
				let result = 0;
				let shift = 0;
				let b;
				do {
					b = byte();
					result += (b & 0x7f) * 2 ** shift; // no shift operators: values can exceed 2^31
					shift += 7;
				} while (b & 0x80);
				return result;
			};
			const name = () => {
				const len = uleb();
				const view = bytes.subarray(off, off + len);
				off += len;
				return decoder.decode(view);
			};
			const limits = () => {
				const flags = byte();
				const l = { shared: (flags & 0x02) !== 0, hasMax: (flags & 0x01) !== 0 };
				l.initial = uleb();
				if (l.hasMax) {
					l.maximum = uleb();
				}
				return l;
			};

			if (bytes.length < 8 || bytes[0] !== 0x00 || bytes[1] !== 0x61 || bytes[2] !== 0x73 || bytes[3] !== 0x6d) {
				throw new Error("provideMemory: not a wasm binary");
			}
			off = 8; // skip magic and version
			while (off < bytes.length) {
				const id = byte();
				const size = uleb();
				const end = off + size;
				if (id === 2) { // import section
					const count = uleb();
					for (let i = 0; i < count; i++) {
						const module = name();
						const field = name();
						const kind = byte();
						switch (kind) {
							case 0x00: // func
								uleb();
								break;
							case 0x01: // table
								byte(); // elem type
								limits();
								break;
							case 0x02: { // memory
								const l = limits();
								if (module === "gojs") {
									return { module: module, name: field, initial: l.initial, hasMax: l.hasMax, maximum: l.maximum, shared: l.shared };
								}
								break;
							}
							case 0x03: // global
								byte(); // value type
								byte(); // mutability
								break;
							default:
								throw new Error("provideMemory: unrecognized import kind " + kind);
						}
					}
					return undefined;
				}
				if (id > 2) {
					// Sections must appear in id order; no import section
					// means no imported memory.
					return undefined;
				}
				off = end;
			}
			return undefined;
		}

		async run(instance) {
			if (!(instance instanceof WebAssembly.Instance)) {
				throw new Error("Go.run: WebAssembly.Instance expected");
			}
			this._inst = instance;
			this._sharedMem = typeof SharedArrayBuffer !== "undefined" && this._mem().buffer instanceof SharedArrayBuffer;
			// A GOWASM=threads module's data segments are passive: applying
			// them on every instantiation would let a worker instance
			// clobber the shared linear memory. The linker emits a
			// synthetic _initmem export that applies (and then drops) the
			// segments; call it here, on the main instance, before any Go
			// code runs. Worker instances never go through Go.run and must
			// never call _initmem. Ordinary modules have no _initmem
			// export and use active segments as always.
			if (this._inst.exports._initmem !== undefined) {
				this._inst.exports._initmem();
			}
			if (this._sharedMem) {
				// GOWASM=threads: a worker thread's Go code can grow the
				// shared memory, and no import is called on THIS instance
				// when that happens, so a plain DataView could go stale.
				// Replace this.mem with an accessor that refreshes the
				// cached view whenever the underlying buffer changed.
				// (Ordinary modules keep the plain property assignment
				// below - non-threads behavior is untouched.)
				let cachedMemView = new DataView(this._mem().buffer);
				Object.defineProperty(this, "mem", {
					configurable: true,
					get: () => {
						const buf = this._mem().buffer;
						if (cachedMemView.buffer !== buf) {
							cachedMemView = new DataView(buf);
						}
						return cachedMemView;
					},
					set: (v) => { cachedMemView = v; },
				});
			}
			this.mem = new DataView(this._mem().buffer);
			this._values = [ // JS values that Go currently has references to, indexed by reference id
				NaN,
				0,
				null,
				true,
				false,
				globalThis,
				this,
			];
			this._goRefCounts = new Array(this._values.length).fill(Infinity); // number of references that Go has to a JS value, indexed by reference id
			this._ids = new Map([ // mapping from JS values to reference ids
				[0, 1],
				[null, 2],
				[true, 3],
				[false, 4],
				[globalThis, 5],
				[this, 6],
			]);
			this._idPool = [];   // unused ids that have been garbage collected
			this.exited = false; // whether the Go program has exited

			// Pass command line arguments and environment variables to WebAssembly by writing them to the linear memory.
			let offset = 4096;

			const strPtr = (str) => {
				const ptr = offset;
				const bytes = encoder.encode(str + "\0");
				new Uint8Array(this.mem.buffer, offset, bytes.length).set(bytes);
				offset += bytes.length;
				if (offset % 8 !== 0) {
					offset += 8 - (offset % 8);
				}
				return ptr;
			};

			const argc = this.argv.length;

			const argvPtrs = [];
			this.argv.forEach((arg) => {
				argvPtrs.push(strPtr(arg));
			});
			argvPtrs.push(0);

			const keys = Object.keys(this.env).sort();
			keys.forEach((key) => {
				argvPtrs.push(strPtr(`${key}=${this.env[key]}`));
			});
			argvPtrs.push(0);

			const argv = offset;
			argvPtrs.forEach((ptr) => {
				this.mem.setUint32(offset, ptr, true);
				this.mem.setUint32(offset + 4, 0, true);
				offset += 8;
			});

			// The linker guarantees global data starts from at least wasmMinDataAddr.
			// Keep in sync with cmd/link/internal/ld/data.go:wasmMinDataAddr.
			const wasmMinDataAddr = 4096 + 61440;
			if (offset >= wasmMinDataAddr) {
				throw new Error(`total length of command line and environment variables exceeds limit (${offset - 4096} bytes needed, ${wasmMinDataAddr - 4096} bytes available)`);
			}

			this._inst.exports.run(argc, argv);
			if (this.exited) {
				this._resolveExitPromise();
			}
			await this._exitPromise;
		}

		// _armMainWake keeps an Atomics.waitAsync armed on the runtime's
		// main-thread wake word (GOWASM=threads). Worker threads bump and
		// notify the word (runtime.wasmWakeMainThread) to wake a main M
		// parked in the event loop; the resolution handler re-arms against
		// the current value (so no wake is ever lost) and resumes the
		// program. The resolution runs as a microtask, i.e. only while the
		// program is paused at the top level, where resume is legal. A
		// pending waitAsync does not keep the Node.js event loop alive, so
		// an idle program can still exit (which is what triggers the
		// exit-time deadlock probe).
		_armMainWake() {
			if (this.exited || this._mainWakeAddr === undefined) {
				return;
			}
			const i32 = new Int32Array(this._mem().buffer);
			const idx = this._mainWakeAddr >> 2;
			const res = Atomics.waitAsync(i32, idx, Atomics.load(i32, idx));
			const promise = res.async ? res.value : Promise.resolve("not-equal");
			promise.then(() => {
				if (this.exited) {
					return;
				}
				this._armMainWake();
				this._resume();
			});
		}

		_resume() {
			if (this.exited) {
				throw new Error("Go program has already exited");
			}
			for (;;) {
				this._inst.exports.resume();
				if (this.exited) {
					this._resolveExitPromise();
					return;
				}
				// GOWASM=threads: while the runtime could not accept an
				// event (its main M had no free P), later events were
				// queued instead of overwriting _pendingEvent (see
				// _makeFuncWrapper). Deliver the next one as soon as the
				// slot frees.
				if (this._pendingEvent !== null || this._eventQueue === undefined || this._eventQueue.length === 0) {
					return;
				}
				this._pendingEvent = this._eventQueue.shift();
			}
		}

		_makeFuncWrapper(id) {
			const go = this;
			return function () {
				const event = { id: id, this: this, args: arguments };
				if (go._pendingEvent !== null && go._sharedMem) {
					// GOWASM=threads: the previous event has not been
					// accepted by the runtime yet (the main M could not
					// take a P before the host regained control).
					// Overwriting _pendingEvent would silently drop that
					// event's Go handler invocation; queue this one
					// instead - its handler runs as soon as the slot
					// frees, and its synchronous JS result is undefined,
					// as for any handler that has not finished when Go
					// pauses. Without threads the slot is always consumed
					// before the wrapper regains control, so this path
					// cannot trigger.
					(go._eventQueue ??= []).push(event);
					return event.result;
				}
				go._pendingEvent = event;
				go._resume();
				return event.result;
			};
		}
	}
})();
