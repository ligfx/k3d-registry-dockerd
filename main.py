import docker
import glob
import hashlib
import tarfile
from flask import Flask, request, send_file

def docker_get_image_tarball(image_id):
    # get the raw response since Docker SDK turns it into an iterator
    res = client.api._get(client.api._url("/images/{0}/get", image_id), stream=True)
    client.api._raise_for_status(res)
    socket = client.api._get_raw_response_socket(res)
    client.api._disable_socket_timeout(socket)
    res.raw.decode_content = True

    class ForwardSeekableWrapper:
        def __init__(self, f):
            self._f = f
            self._p = 0
        def tell(self):
            return self._p
        def read(self, size=None):
            result = self._f.read(size)
            self._p += len(result)
            return result
        def seek(self, where):
            if where >= self._p:
                self.read(where - self._p)
                return
            raise NotImplementedError(self._p, where)
    
    return ForwardSeekableWrapper(res.raw)

app = Flask(__name__)
client = docker.from_env()

@app.route("/")
def hello_world():
    return "Hello, world!"

@app.route("/v2/")
def v2():
    # return ('', 204)
    return "Hello, world!"

def find_blob(domain, namespace, image, reference, mimetype):
    if not domain:
        # don't support acting as own repo
        return ('', 404)
    
    if not reference.startswith('sha256:'):
        raise NotImplementedError("Bad reference %r" % reference)
    
    # pull from Docker's export cache, if it exists
    # TODO: pull from Docker's containerd storage (overlayfs?), if that storage engine is being used
    cached = glob.glob(f'/var/lib/docker/tmp/docker-export-*/blobs/sha256/{reference.split("sha256:")[1]}')
    if cached:
        app.logger.info(f'using cached file {cached[0]}')
        return send_file(cached[0], mimetype=mimetype if mimetype else 'application/octet-stream')

    # go pull some tarballs and take a look
    name=f'{domain}/{namespace}/{image}'
    if name.startswith('docker.io/'):
        name = name[len('docker.io/'):]
    for image_obj in client.images.list(name):
        # TODO: could probably be smarter and keep a cache of which blobs belong to which images?
        t = tarfile.TarFile(fileobj=docker_get_image_tarball(image_obj.id))
        while True:
            ti = t.next()
            if ti is None:
                break
            if ti.name == f'blobs/sha256/{reference.split("sha256:")[1]}':
                # TODO: could also get mimetype by trying blobs that look like JSON / come from
                # a manifest request, and use the embedded mediaType
                resp = send_file(t.extractfile(ti), mimetype=mimetype if mimetype else 'application/octet-stream')
                resp.headers.add('content-length', ti.size)
                return resp
            if ti.name == 'index.json':
                fi = t.extractfile(ti)
                bdata = fi.read()
                m = hashlib.sha256()
                m.update(bdata)
                app.logger.info("trying index.json, digest %r" % m.hexdigest())
                if m.hexdigest() == reference.split("sha256:")[1]:
                    return (bdata, 200, {"content-type": "application/vnd.oci.image.index.v1+json"})
                
    return ('No blob found', 404)

@app.route("/v2/<namespace>/<image>/blobs/<reference>")
def blobs(namespace, image, reference):
    if request.method != 'GET':
        raise NotImplementedError()

    domain = request.args.get('ns')
    app.logger.info(repr(request.headers))

    if not domain:
        # don't support acting as own repo
        return ("Requires a 'ns' query param", 500)

    mimetype = request.headers.get('accept').split(',')[0].strip()
    return find_blob(domain, namespace, image, reference, mimetype=mimetype)

@app.route("/v2/<namespace>/<image>/manifests/<reference>")
def manifests(namespace, image, reference):
    if request.method not in ('HEAD', 'GET'):
        raise ('', 405)

    app.logger.info(repr(request.headers))

    domain = request.args.get('ns')
    if not domain:
        # don't support acting as own repo
        return ('', 404)

    # TODO: check accept header

    if reference.startswith('sha256:'):
        mimetype = request.headers.get('accept').split(',')[0].strip()
        return find_blob(domain, namespace, image, reference, mimetype=mimetype)

    def find_image():
        name=f'{domain}/{namespace}/{image}'
        if name.startswith('docker.io/'):
            name = name[len('docker.io/'):]
        for i in client.images.list(name):
            for t in i.tags:
                if t.count(":") != 1:
                    raise Exception(t)
                if t.split(":")[1] == reference:
                    return i
        return client.images.pull(name, tag=reference)

    image_obj = find_image()
    if not image_obj:
        return ('', 404)

    if request.method == 'HEAD':
        return ('', 200)

    # this tarfile is in OCI image format
    t = tarfile.TarFile(fileobj=docker_get_image_tarball(image_obj.id))
    while True:
        ti = t.next()
        if ti is None:
            break
        app.logger.info(ti)
        if ti.name == 'index.json':
            bdata = t.extractfile(ti).read()
            m = hashlib.sha256()
            m.update(bdata)
            digest = m.hexdigest()
            app.logger.info(digest)
            return (bdata, 200, {"content-type": "application/vnd.oci.image.index.v1+json"})
    return ('No manifest found', 500)

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, debug=True)